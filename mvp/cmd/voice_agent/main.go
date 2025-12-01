package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"concierge/internal/handlers"
	"concierge/internal/models"
	"concierge/internal/service"
)

func main() {
	fmt.Println("☎️  Voice Agent Service - Голосовой агент на базе Yandex и Asterisk")

	config, err := models.LoadConfig()
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфигурации: %v", err)
	}

	callService, err := service.NewCallService(config)
	if err != nil {
		log.Fatalf("❌ Ошибка инициализации сервиса: %v", err)
	}
	defer callService.Close()

	callHandler := handlers.NewCallHandler(callService)

	mux := http.NewServeMux()
	mux.HandleFunc("/call/start", callHandler.HandleCallStart)

	server := &http.Server{
		Addr:    ":" + config.HTTPPort,
		Handler: mux,
	}

	go func() {
		log.Printf("🚀 HTTP сервер запущен на порту %s\n", config.HTTPPort)
		log.Printf("📡 Endpoint: http://localhost:%s/call/start?phone_number=79914043003\n", config.HTTPPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Ошибка HTTP сервера: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("\n⚠️  Получен сигнал остановки, завершаем работу...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("⚠️  Ошибка при остановке HTTP сервера: %v", err)
	}

	log.Println("✅ Сервер остановлен")
}
