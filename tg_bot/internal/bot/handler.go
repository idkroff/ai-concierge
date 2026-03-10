package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"tg_bot/internal/usecase"

	tele "gopkg.in/telebot.v3"
)

const (
	callbackConfirm = "confirm"
	callbackCancel  = "cancel"

	handlerTimeout = 30 * time.Second

	// Лимит текста в одном Telegram-сообщении
	maxMessageLen = 3800
)

type Handler struct {
	uc  *usecase.CallUsecase
	ctx context.Context
}

func NewHandler(uc *usecase.CallUsecase, ctx context.Context) *Handler {
	return &Handler{uc: uc, ctx: ctx}
}

func (h *Handler) Register(b *tele.Bot) {
	btnConfirm := tele.Btn{Text: "✅ Подтвердить", Unique: callbackConfirm}
	btnCancel := tele.Btn{Text: "❌ Отмена", Unique: callbackCancel}

	b.Handle("/start", h.onStart)

	b.Handle(tele.OnText, func(c tele.Context) error {
		return h.onMessage(c, btnConfirm, btnCancel)
	})

	b.Handle(&btnConfirm, h.onConfirm)
	b.Handle(&btnCancel, h.onCancel)
}

func (h *Handler) onStart(c tele.Context) error {
	return c.Send("Привет! Я ИИ-консьерж — могу позвонить ваши рутинные задачи за вас.\n\nПросто напишите, кому и зачем нужно позвонить, например:\n<i>7 995 123 45-67 забронируй столик у окна на 19:00</i>", tele.ModeHTML)
}

func (h *Handler) onMessage(c tele.Context, btnConfirm, btnCancel tele.Btn) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	req, err := h.uc.HandleMessage(ctx, c.Sender().ID, c.Text())
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

	result, err := h.uc.ConfirmCall(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("ConfirmCall error: %v", err)
		_ = c.Edit("Не удалось инициировать звонок: " + err.Error())
		return nil
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

	if err := h.uc.CancelCall(ctx, c.Sender().ID); err != nil {
		log.Printf("CancelCall error: %v", err)
	}
	return c.Edit("❌ Звонок отменён.")
}

// streamUpdates отображает обновления звонка в Telegram — только форматирование и API-вызовы.
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

// renderStatus форматирует текущее состояние звонка в HTML для Telegram.
func renderStatus(shortID string, upd usecase.CallUpdate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📞 Звонок <code>%s</code>\n\n", shortID)

	// Рендерим транскрипт, обрезая старые реплики если слишком длинно
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

// renderTranscript форматирует список реплик, обрезая с начала если превышен лимит.
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

	// Обрезаем с начала пока не уложимся в лимит
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
