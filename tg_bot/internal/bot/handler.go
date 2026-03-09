package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	"tg_bot/internal/usecase"

	tele "gopkg.in/telebot.v3"
)

const (
	callbackConfirm = "confirm"
	callbackCancel  = "cancel"

	handlerTimeout = 30 * time.Second
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

	b.Handle(tele.OnText, func(c tele.Context) error {
		return h.onMessage(c, btnConfirm, btnCancel)
	})

	b.Handle(&btnConfirm, h.onConfirm)
	b.Handle(&btnCancel, h.onCancel)
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

	return c.Edit(fmt.Sprintf("✅ Звонок инициирован!\nID: <code>%s</code>", result.CallID), tele.ModeHTML)
}

func (h *Handler) onCancel(c tele.Context) error {
	ctx, cancel := context.WithTimeout(h.ctx, handlerTimeout)
	defer cancel()

	if err := h.uc.CancelCall(ctx, c.Sender().ID); err != nil {
		log.Printf("CancelCall error: %v", err)
	}
	return c.Edit("❌ Звонок отменён.")
}
