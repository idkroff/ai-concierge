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
	"concierge/internal/parser"
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

	p := parser.New(config.APIKey, config.Folder)
	callHandler := handlers.NewCallHandler(callService, p)
	wsHandler := handlers.NewWSHandler(callService)

	mux := http.NewServeMux()
	mux.HandleFunc("/call/start", callHandler.HandleCallStart)
	mux.HandleFunc("/ws", wsHandler.ServeWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":" + config.HTTPPort,
		Handler: mux,
	}

	go func() {
		log.Printf("🚀 HTTP сервер запущен на порту %s\n", config.HTTPPort)
		log.Printf("📡 GET /call/start?phone_number=79914043003 — старт звонка (без событий)\n")
		log.Printf("📡 WS /ws — подключение, затем {\"action\":\"start_call\",\"phone_number\":\"7999...\"} — поток событий\n")
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
