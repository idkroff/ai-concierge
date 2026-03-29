package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
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

	welcomeText     = "Я ИИ-консьерж — могу позвонить по вашим рутинным задачам за вас.\n\nПросто напишите, кому и зачем нужно позвонить, например:\n<i>7 995 123 45-67 забронируй столик у окна на 19:00</i>"
	phoneOfferText  = "\n\nКстати, вы можете оставить свой номер телефона, чтобы мы могли связаться с вами по вашим задачам. Для этого нажмите кнопку ниже."
	phoneSavedText  = "\n\n✅ Ваш номер телефона сохранён."
)

type Handler struct {
	callUC    *usecase.CallUsecase
	userUC    *usecase.UserUsecase
	usedCalls repo.UsedCallsRepository
	ctx       context.Context
}

func NewHandler(callUC *usecase.CallUsecase, userUC *usecase.UserUsecase, usedCalls repo.UsedCallsRepository, ctx context.Context) *Handler {
	return &Handler{callUC: callUC, userUC: userUC, usedCalls: usedCalls, ctx: ctx}
}

func (h *Handler) Register(b *tele.Bot) {
	btnConfirm := tele.Btn{Text: "✅ Подтвердить", Unique: callbackConfirm}
	btnCancel := tele.Btn{Text: "❌ Отмена", Unique: callbackCancel}

	b.Handle("/start", h.onStart)
	b.Handle(tele.OnContact, h.onContact)

	b.Handle(tele.OnText, func(c tele.Context) error {
		return h.onText(c, btnConfirm, btnCancel)
	})

	b.Handle(&btnConfirm, h.onConfirm)
	b.Handle(&btnCancel, h.onCancel)
}

func (h *Handler) onStart(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	user, err := h.userUC.EnsureUser(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("EnsureUser error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}

	if user.Name != "" {
		return h.sendWelcome(c, user, fmt.Sprintf("С возвращением, %s! ", html.EscapeString(user.Name)))
	}

	return c.Send("Привет! Как вас представлять?")
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

	if err := h.userUC.SavePhone(ctx, c.Sender().ID, contact.PhoneNumber); err != nil {
		log.Printf("SavePhone error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}

	rm := &tele.ReplyMarkup{RemoveKeyboard: true}
	return c.Send("Спасибо, ваш номер телефона сохранён!", rm)
}

func (h *Handler) sendWelcome(c tele.Context, user entity.User, greeting string) error {
	text := greeting + welcomeText
	if user.Phone != "" {
		text += phoneSavedText
		return c.Send(text, tele.ModeHTML)
	}

	text += phoneOfferText
	menu := &tele.ReplyMarkup{ResizeKeyboard: true, OneTimeKeyboard: true}
	btnPhone := menu.Contact("📱 Поделиться номером телефона")
	menu.Reply(menu.Row(btnPhone))
	return c.Send(text, menu, tele.ModeHTML)
}

func (h *Handler) onText(c tele.Context, btnConfirm, btnCancel tele.Btn) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	user, err := h.userUC.EnsureUser(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("EnsureUser error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}

	if user.Name == "" {
		name := strings.TrimSpace(c.Text())
		if name == "" {
			return c.Send("Имя не может быть пустым. Как вас представлять?")
		}
		if err := h.userUC.SaveName(ctx, c.Sender().ID, name); err != nil {
			log.Printf("SaveName error: %v", err)
			return c.Send("Произошла ошибка. Попробуйте ещё раз.")
		}
		user.Name = name
		return h.sendWelcome(c, user, fmt.Sprintf("Приятно познакомиться, %s! ", html.EscapeString(name)))
	}

	return h.onMessage(c, btnConfirm, btnCancel)
}

func (h *Handler) onMessage(c tele.Context, btnConfirm, btnCancel tele.Btn) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	req, err := h.callUC.HandleMessage(ctx, c.Sender().ID, c.Text())
	if err != nil {
		log.Printf("HandleMessage error: %v", err)
		return c.Send("Произошла ошибка. Попробуйте ещё раз.")
	}

	kb := &tele.ReplyMarkup{}
	kb.Inline(kb.Row(btnConfirm, btnCancel))

	text := fmt.Sprintf("Хотите совершить звонок с запросом:\n\n<i>%s</i>", req.Message)
	return c.Send(text, kb, tele.ModeHTML)
}

func (h *Handler) onConfirm(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	result, err := h.callUC.ConfirmCall(ctx, c.Sender().ID)
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

	go h.streamUpdates(c.Bot(), c.Chat(), statusMsg, shortID, result.Updates)

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

func (h *Handler) streamUpdates(bot *tele.Bot, chat *tele.Chat, statusMsg *tele.Message, shortID string, updates <-chan usecase.CallUpdate) {
	for upd := range updates {
		if upd.Ended {
			finalText := renderStatus(shortID, upd) + "\n📵 <b>Звонок завершён</b>"
			_, _ = bot.Edit(statusMsg, finalText, tele.ModeHTML)
			if upd.Error != "" {
				_, _ = bot.Send(chat, "Ошибка: "+upd.Error)
			} else {
				_, _ = bot.Send(chat, "Причина завершения: "+formatReason(upd.EndReason))
			}
			return
		}

		text := renderStatus(shortID, upd)
		if _, err := bot.Edit(statusMsg, text, tele.ModeHTML); err != nil {
			log.Printf("[stream] edit message: %v", err)
		}
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
