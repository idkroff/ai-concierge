package bot

import (
	"context"
	"fmt"
	"log"

	"tg_bot/internal/usecase"

	tele "gopkg.in/telebot.v3"
)

const (
	callbackConfirm = "confirm"
	callbackCancel  = "cancel"
)

type Handler struct {
	uc *usecase.CallUsecase
}

func NewHandler(uc *usecase.CallUsecase) *Handler {
	return &Handler{uc: uc}
}

func (h *Handler) Register(b *tele.Bot) {
	btnConfirm := tele.Btn{Text: "✅ Подтвердить", Unique: callbackConfirm}
	btnCancel := tele.Btn{Text: "❌ Отмена", Unique: callbackCancel}

	b.Handle(tele.OnText, func(c tele.Context) error {
		return h.onMessage(c, btnConfirm, btnCancel)
	})

	b.Handle(&btnConfirm, h.onConfirm)
	b.Handle(&btnCancel, h.onCancel)
}

func (h *Handler) onMessage(c tele.Context, btnConfirm, btnCancel tele.Btn) error {
	req, err := h.uc.HandleMessage(context.Background(), c.Sender().ID, c.Text())
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
	result, err := h.uc.ConfirmCall(context.Background(), c.Sender().ID)
	if err != nil {
		log.Printf("ConfirmCall error: %v", err)
		_ = c.Edit("Не удалось инициировать звонок: " + err.Error())
		return nil
	}

	return c.Edit(fmt.Sprintf("✅ Звонок инициирован!\nID: <code>%s</code>", result.CallID), tele.ModeHTML)
}

func (h *Handler) onCancel(c tele.Context) error {
	if err := h.uc.CancelCall(context.Background(), c.Sender().ID); err != nil {
		log.Printf("CancelCall error: %v", err)
	}
	return c.Edit("❌ Звонок отменён.")
}
