package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"concierge/internal/models"
	"concierge/internal/parser"
	"concierge/internal/service"

	"github.com/google/uuid"
)

type CallHandler struct {
	service *service.CallService
	parser  *parser.Parser
}

func NewCallHandler(service *service.CallService, parser *parser.Parser) *CallHandler {
	return &CallHandler{
		service: service,
		parser:  parser,
	}
}

func (h *CallHandler) HandleCallStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendError(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	message := r.URL.Query().Get("message")
	if message == "" {
		h.sendError(w, "Параметр message обязателен", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	parsed, err := h.parser.Parse(ctx, message)
	if err != nil {
		log.Printf("❌ Ошибка парсинга сообщения: %v\n", err)
		h.sendError(w, err.Error(), http.StatusBadRequest)
		return
	}

	callID := uuid.New().String()

	response := models.CallStartResponse{
		Status: "ok",
		CallID: callID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	log.Printf("[%s] 🚀 Звонок инициирован через API на номер %s\n", callID, parsed.PhoneNumber)

	go h.service.HandleCall(callID, parsed.PhoneNumber, parsed.Context)
}

func (h *CallHandler) sendError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(models.ErrorResponse{
		Error: message,
	})
}
