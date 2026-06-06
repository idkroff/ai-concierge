package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"tg_bot/internal/domain/entity"
	"tg_bot/internal/domain/repo"
	"tg_bot/internal/usecase"

	tele "gopkg.in/telebot.v3"
)

const (
	callbackConfirm = "confirm"
	callbackCancel  = "cancel"

	handlerTimeout = 30 * time.Second

	maxMessageLen = 3800

	dailyCallLimit = 3

	askPhoneText = "Привет! Я ИИ-консьерж — звоню по вашим рутинным задачам за вас.\n\nЧтобы начать, поделитесь своим номером телефона — нажмите кнопку ниже."
	readyText    = "Готов звонить 🙌\n\nНапишите, кому и зачем нужно позвонить, например:\n<i>тануки на таганской забронируй столик на 19:00</i>\nили\n<i>+7 495 739-00-33 узнай часы работы</i>\n\nИмя, от которого я звоню, можно задать командой /name."
	limitText    = "Достигнут дневной лимит звонков (3). Попробуйте завтра 🌙"
)

var nonDigitRe = regexp.MustCompile(`\D`)

// номера (11 цифр) с доступом к /droplimits
var droplimitsWhitelist = map[string]bool{
	"79914043003": true,
}

type Handler struct {
	callUC    *usecase.CallUsecase
	userUC    *usecase.UserUsecase
	usedCalls repo.UsedCallsRepository
	ctx       context.Context

	mu      sync.Mutex
	pending map[int64]*pendingClarification
}

type pendingClarification struct {
	clarID   string
	deadline time.Time
}

const clarificationWindow = 28 * time.Second // чуть меньше 30с на стороне caller

func NewHandler(callUC *usecase.CallUsecase, userUC *usecase.UserUsecase, usedCalls repo.UsedCallsRepository, ctx context.Context) *Handler {
	return &Handler{
		callUC:    callUC,
		userUC:    userUC,
		usedCalls: usedCalls,
		ctx:       ctx,
		pending:   make(map[int64]*pendingClarification),
	}
}

func (h *Handler) Register(b *tele.Bot) {
	btnConfirm := tele.Btn{Text: "✅ Подтвердить", Unique: callbackConfirm}
	btnCancel := tele.Btn{Text: "❌ Отмена", Unique: callbackCancel}

	b.Handle("/start", h.onStart)
	b.Handle("/help", h.onHelp)
	b.Handle("/name", h.onName)
	b.Handle("/interactive", h.onInteractive)
	b.Handle("/droplimits", h.onDropLimits)
	b.Handle(tele.OnContact, h.onContact)

	b.Handle(tele.OnText, func(c tele.Context) error {
		return h.onText(c, btnConfirm, btnCancel)
	})

	b.Handle(&btnConfirm, h.onConfirm)
	b.Handle(&btnCancel, h.onCancel)

	// /droplimits намеренно не публикуем — админская
	if err := b.SetCommands([]tele.Command{
		{Text: "start", Description: "Запуск и краткая справка"},
		{Text: "help", Description: "Помощь"},
		{Text: "name", Description: "Имя, от которого я звоню"},
		{Text: "interactive", Description: "Доуточнение во время звонка вкл/выкл"},
	}); err != nil {
		log.Printf("SetCommands error: %v", err)
	}
}

// возвращает false и просит поделиться номером, если пользователь не зарегистрирован
func (h *Handler) requirePhone(ctx context.Context, c tele.Context) (entity.User, bool) {
	user, err := h.userUC.EnsureUser(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("EnsureUser error: %v", err)
		_ = c.Send("Произошла ошибка. Попробуйте ещё раз.")
		return entity.User{}, false
	}
	if user.Phone == "" {
		_ = h.askPhone(c)
		return entity.User{}, false
	}
	return user, true
}

func (h *Handler) askPhone(c tele.Context) error {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true, OneTimeKeyboard: true}
	btnPhone := menu.Contact("📱 Поделиться номером телефона")
	menu.Reply(menu.Row(btnPhone))
	return c.Send(askPhoneText, menu, tele.ModeHTML)
}

func (h *Handler) onStart(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	user, err := h.userUC.EnsureUser(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("EnsureUser error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}

	if user.Phone == "" {
		return h.askPhone(c)
	}
	return c.Send(readyText, tele.ModeHTML)
}

func (h *Handler) onHelp(c tele.Context) error {
	return c.Send(readyText, tele.ModeHTML)
}

func (h *Handler) onContact(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	contact := c.Message().Contact
	if contact == nil {
		return nil
	}
	if contact.UserID != c.Sender().ID {
		return c.Send("Пожалуйста, поделитесь своим номером телефона, а не чужим контактом.")
	}

	phone, ok := normalizePhone(contact.PhoneNumber)
	if !ok {
		return c.Send("Не удалось распознать номер. Попробуйте ещё раз через кнопку ниже.")
	}
	if err := h.userUC.SavePhone(ctx, c.Sender().ID, phone); err != nil {
		log.Printf("SavePhone error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}

	rm := &tele.ReplyMarkup{RemoveKeyboard: true}
	return c.Send("✅ Номер сохранён.\n\n"+readyText, rm, tele.ModeHTML)
}

func (h *Handler) onName(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	user, ok := h.requirePhone(ctx, c)
	if !ok {
		return nil
	}

	name := strings.TrimSpace(c.Message().Payload)
	if name == "" {
		if user.Name != "" {
			return c.Send(fmt.Sprintf("Сейчас я звоню от имени: <b>%s</b>.\nЧтобы изменить — пришлите: <code>/name Иван</code>", html.EscapeString(user.Name)), tele.ModeHTML)
		}
		return c.Send("Укажите имя так: <code>/name Иван</code>.\nЯ буду представляться им в звонках от вашего имени.", tele.ModeHTML)
	}
	if err := h.userUC.SaveName(ctx, c.Sender().ID, name); err != nil {
		log.Printf("SaveName error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}
	return c.Send(fmt.Sprintf("✅ Имя сохранено: <b>%s</b>", html.EscapeString(name)), tele.ModeHTML)
}

// /interactive — вкл/выкл доуточнение у клиента во время звонка
func (h *Handler) onInteractive(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	user, ok := h.requirePhone(ctx, c)
	if !ok {
		return nil
	}

	newVal := !user.InteractiveMode
	if err := h.userUC.SaveInteractiveMode(ctx, c.Sender().ID, newVal); err != nil {
		log.Printf("SaveInteractiveMode error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}
	if newVal {
		return c.Send("✅ Интерактивный режим включён.\n\nЕсли во время звонка я не буду знать ответ — спрошу вас прямо здесь. Ответьте сообщением в течение 30 секунд, и я продолжу разговор.")
	}
	return c.Send("Интерактивный режим выключен.")
}

// /droplimits — сброс своего дневного счётчика; только для droplimitsWhitelist
func (h *Handler) onDropLimits(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	user, ok := h.requirePhone(ctx, c)
	if !ok {
		return nil
	}

	phone, _ := normalizePhone(user.Phone)
	if !droplimitsWhitelist[phone] {
		return c.Send("Команда недоступна.")
	}

	if err := h.usedCalls.Reset(ctx, c.Sender().ID); err != nil {
		log.Printf("Reset limits error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}
	return c.Send("✅ Дневной лимит сброшен.")
}

func (h *Handler) onText(c tele.Context, btnConfirm, btnCancel tele.Btn) error {
	// Если ждём ответ на доуточнение во время звонка — перехватываем этот текст.
	if h.tryAnswerClarification(c) {
		return nil
	}

	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	if _, ok := h.requirePhone(ctx, c); !ok {
		return nil
	}
	return h.onMessage(c, btnConfirm, btnCancel)
}

// tryAnswerClarification перехватывает текст как ответ на активный вопрос агента; true — если обработано.
func (h *Handler) tryAnswerClarification(c tele.Context) bool {
	userID := c.Sender().ID

	h.mu.Lock()
	pc, ok := h.pending[userID]
	if ok {
		delete(h.pending, userID)
	}
	h.mu.Unlock()

	if !ok || time.Now().After(pc.deadline) {
		if ok {
			log.Printf("[clarify] user=%d ответ пришёл, но окно истекло (clar=%s)", userID, pc.clarID)
		}
		return false
	}

	answer := strings.TrimSpace(c.Text())
	if answer == "" {
		return false
	}
	log.Printf("[clarify] user=%d перехват ответа на clar=%s: %q", userID, pc.clarID, answer)

	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	if err := h.callUC.AnswerClarification(ctx, userID, pc.clarID, answer); err != nil {
		log.Printf("AnswerClarification error: %v", err)
		_ = c.Send("Не удалось передать ответ — возможно, звонок уже завершился.")
		return true
	}
	_ = c.Send("✅ Передал ваш ответ.")
	return true
}

// armClarification взводит ожидание ответа пользователя на вопрос агента.
func (h *Handler) armClarification(userID int64, clarID string) {
	h.mu.Lock()
	h.pending[userID] = &pendingClarification{clarID: clarID, deadline: time.Now().Add(clarificationWindow)}
	h.mu.Unlock()

	time.AfterFunc(clarificationWindow, func() {
		h.mu.Lock()
		if pc, ok := h.pending[userID]; ok && pc.clarID == clarID {
			delete(h.pending, userID)
		}
		h.mu.Unlock()
	})
}

func (h *Handler) onMessage(c tele.Context, btnConfirm, btnCancel tele.Btn) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	if cnt, err := h.usedCalls.GetTodayCount(ctx, c.Sender().ID); err != nil {
		log.Printf("GetTodayCount error: %v", err)
	} else if cnt >= dailyCallLimit {
		return c.Send(limitText)
	}

	// резолв номера может занять секунды — показываем статус, потом превращаем в подтверждение
	searching, _ := c.Bot().Send(c.Recipient(), "🔎 Определяю номер телефона…")

	req, err := h.callUC.HandleMessage(ctx, c.Sender().ID, c.Text())
	if err != nil {
		log.Printf("HandleMessage error: %v", err)
		msg := "Не получилось определить номер телефона. Укажите номер явно или уточните название организации, например:\n<i>тануки на таганской забронируй столик на 19:00</i>"
		if searching != nil {
			_, _ = c.Bot().Edit(searching, msg, tele.ModeHTML)
			return nil
		}
		return c.Send(msg, tele.ModeHTML)
	}

	kb := &tele.ReplyMarkup{}
	kb.Inline(kb.Row(btnConfirm, btnCancel))

	var b strings.Builder
	if req.Organization != "" {
		fmt.Fprintf(&b, "📍 <b>%s</b>\n", html.EscapeString(req.Organization))
	}
	fmt.Fprintf(&b, "📞 <code>%s</code>", formatPhone(req.PhoneNumber))
	if req.IsHotline {
		b.WriteString(" ⚠️ <i>федеральная линия</i>")
	}
	if req.DisplayName != "" {
		fmt.Fprintf(&b, "\nℹ️ %s", html.EscapeString(req.DisplayName))
	}
	fmt.Fprintf(&b, "\n\nЗапрос: <i>%s</i>\n\nПозвонить?", html.EscapeString(req.Context))
	text := b.String()

	if searching != nil {
		_, err = c.Bot().Edit(searching, text, kb, tele.ModeHTML)
		return err
	}
	return c.Send(text, kb, tele.ModeHTML)
}

// 79991234567 -> +7 (999) 123-45-67
func formatPhone(d string) string {
	if len(d) != 11 {
		return d
	}
	return fmt.Sprintf("+%s (%s) %s-%s-%s", d[0:1], d[1:4], d[4:7], d[7:9], d[9:11])
}

// приводит номер к 11 цифрам (8 -> 7); ok=false, если не получилось
func normalizePhone(raw string) (string, bool) {
	d := nonDigitRe.ReplaceAllString(raw, "")
	switch {
	case len(d) == 11 && d[0] == '8':
		d = "7" + d[1:]
	case len(d) == 10:
		d = "7" + d
	}
	if len(d) != 11 {
		return "", false
	}
	return d, true
}

func (h *Handler) onConfirm(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	if cnt, err := h.usedCalls.GetTodayCount(ctx, c.Sender().ID); err != nil {
		log.Printf("GetTodayCount error: %v", err)
	} else if cnt >= dailyCallLimit {
		_ = c.Edit(limitText)
		return nil
	}

	user, err := h.userUC.EnsureUser(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("EnsureUser error: %v", err)
		_ = c.Edit("Произошла ошибка. Попробуйте ещё раз.")
		return nil
	}

	result, err := h.callUC.ConfirmCall(ctx, c.Sender().ID, user.Name, user.InteractiveMode)
	if err != nil {
		log.Printf("ConfirmCall error: %v", err)
		_ = c.Edit("Не удалось инициировать звонок: " + err.Error())
		return nil
	}

	if err := h.usedCalls.Increment(ctx, c.Sender().ID); err != nil {
		log.Printf("IncrementCallCount error: %v", err)
	}

	shortID := result.CallID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	statusMsg := c.Message()
	_ = c.Edit(fmt.Sprintf("📞 Звонок <code>%s</code>\n\n⏳ Подключение...", shortID), tele.ModeHTML)

	go h.streamUpdates(c.Bot(), c.Chat(), c.Sender().ID, statusMsg, shortID, result.Updates)

	return nil
}

func (h *Handler) onCancel(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	if err := h.callUC.CancelCall(ctx, c.Sender().ID); err != nil {
		log.Printf("CancelCall error: %v", err)
	}
	return c.Edit("❌ Звонок отменён.")
}

func (h *Handler) streamUpdates(bot *tele.Bot, chat *tele.Chat, userID int64, statusMsg *tele.Message, shortID string, updates <-chan usecase.CallUpdate) {
	for upd := range updates {
		// Вопрос агента клиенту — отдельным сообщением + взводим ожидание ответа.
		if upd.ClarificationQuestion != "" {
			log.Printf("[clarify] user=%d вопрос от агента clar=%s: %q", userID, upd.ClarificationID, upd.ClarificationQuestion)
			h.armClarification(userID, upd.ClarificationID)
			_, _ = bot.Send(chat, "❓ "+upd.ClarificationQuestion+"\n\n<i>Ответьте сообщением в течение 30 секунд — я продолжу разговор.</i>", tele.ModeHTML)
			continue
		}
		if upd.Notice != "" {
			_, _ = bot.Send(chat, upd.Notice)
			continue
		}

		if upd.Ended {
			if upd.Error != "" {
				_, _ = bot.Edit(statusMsg, renderStatus(shortID, upd)+"\n📵 <b>Звонок завершён</b>", tele.ModeHTML)
				_, _ = bot.Send(chat, "Ошибка: "+upd.Error)
				return
			}
			if upd.Summary != "" {
				finalText := summaryEmoji(upd.SummaryStatus) + " <b>Итог звонка:</b>\n" + html.EscapeString(upd.Summary)
				// для ненормальной концовки добавляем короткую причину
				if upd.EndReason != "farewell" {
					finalText += "\n\n<i>" + formatReason(upd.EndReason) + "</i>"
				}
				_, _ = bot.Edit(statusMsg, finalText, tele.ModeHTML)
				return
			}
			// итога нет (LLM не ответил) — показываем транскрипцию как раньше
			_, _ = bot.Edit(statusMsg, renderStatus(shortID, upd)+"\n📵 <b>Звонок завершён</b>", tele.ModeHTML)
			_, _ = bot.Send(chat, "Причина завершения: "+formatReason(upd.EndReason))
			return
		}

		text := renderStatus(shortID, upd)
		if _, err := bot.Edit(statusMsg, text, tele.ModeHTML); err != nil {
			log.Printf("[stream] edit message: %v", err)
		}
	}
}

func summaryEmoji(status string) string {
	switch status {
	case "success":
		return "✅"
	case "partial":
		return "⚠️"
	case "failed":
		return "❌"
	default:
		return "ℹ️"
	}
}

func renderStatus(shortID string, upd usecase.CallUpdate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📞 Звонок <code>%s</code>\n\n", shortID)

	transcriptText := renderTranscript(upd.Transcript)
	b.WriteString(transcriptText)

	if upd.AgentStreaming != "" {
		b.WriteString("🤖 " + upd.AgentStreaming + "▌\n")
	}
	if upd.AbonentSpeaking {
		b.WriteString("👤 <i>[говорит...]</i>\n")
	}

	return b.String()
}

func renderTranscript(entries []usecase.TranscriptEntry) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		switch e.Role {
		case usecase.RoleSystem:
			lines = append(lines, "☎️ <b>"+e.Text+"</b>")
		case usecase.RoleAgent:
			lines = append(lines, "🤖 "+e.Text)
		case usecase.RoleCallee:
			lines = append(lines, "👤 "+e.Text)
		}
	}

	text := strings.Join(lines, "\n") + "\n"
	for len(text) > maxMessageLen && len(lines) > 1 {
		lines = lines[1:]
		text = "...\n" + strings.Join(lines, "\n") + "\n"
	}
	return text
}

func formatReason(reason string) string {
	switch reason {
	case "farewell":
		return "агент попрощался"
	case "abonent_hangup":
		return "абонент положил трубку"
	case "timeout":
		return "таймаут (5 минут)"
	case "cancelled":
		return "отменён"
	default:
		return reason
	}
}
