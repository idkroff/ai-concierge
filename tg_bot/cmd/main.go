package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
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
		Poller: &tele.LongPoller{Timeout: 3 * time.Second},
	})
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	h := bot.NewHandler(uc, ctx)
	h.Register(b)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go b.Start()
	log.Println("Bot started")

	<-sigChan
	log.Println("Shutting down...")
	cancel()
	b.Stop()

	log.Println("Graceful shutdown complete")
}
