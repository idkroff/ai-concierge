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
	ydbRepo "tg_bot/internal/repo/ydb"
	"tg_bot/internal/usecase"

	tele "gopkg.in/telebot.v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ydbClient, err := ydbRepo.NewClient(ctx, cfg.YDBDSN, cfg.YDBSAKeyFile)
	if err != nil {
		log.Fatalf("ydb connect: %v", err)
	}
	defer func() { _ = ydbClient.Close(ctx) }()

	if err := ydbClient.EnsureTables(ctx); err != nil {
		log.Fatalf("ydb ensure tables: %v", err)
	}

	sessionRepo := memory.NewSessionRepository()
	userRepo := ydbRepo.NewUserRepository(ydbClient)
	usedCallsRepo := ydbRepo.NewUsedCallsRepository(ydbClient)
	callerClient := caller.NewClient(cfg.CallerServiceURL)

	callUC := usecase.NewCallUsecase(sessionRepo, callerClient)
	userUC := usecase.NewUserUsecase(userRepo)

	b, err := tele.NewBot(tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{Timeout: 3 * time.Second},
	})
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}

	h := bot.NewHandler(callUC, userUC, usedCallsRepo, ctx)
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
