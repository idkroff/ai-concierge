package main

import (
	"log"
	"time"

	"tg_bot/config"
	"tg_bot/internal/bot"
	"tg_bot/internal/caller"
	"tg_bot/internal/repo/memory"
	"tg_bot/internal/usecase"

	tele "gopkg.in/telebot.v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	sessionRepo := memory.NewSessionRepository()
	callerClient := caller.NewClient(cfg.CallerServiceURL)
	uc := usecase.NewCallUsecase(sessionRepo, callerClient)

	b, err := tele.NewBot(tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}

	h := bot.NewHandler(uc)
	h.Register(b)

	log.Println("Bot started")
	b.Start()
}
