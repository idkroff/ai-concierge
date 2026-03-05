package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"concierge/internal/events"
	"concierge/internal/models"
	"concierge/internal/service"

	"github.com/google/uuid"
)

type CallHandler struct {
	service *service.CallService
}

func NewCallHandler(service *service.CallService) *CallHandler {
	return &CallHandler{
		service: service,
	}
}

func (h *CallHandler) HandleCallStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendError(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	phoneNumber := r.URL.Query().Get("phone_number")
	if phoneNumber == "" {
		h.sendError(w, "Параметр phone_number обязателен", http.StatusBadRequest)
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

	log.Printf("[%s] 🚀 Звонок инициирован через API на номер %s\n", callID, phoneNumber)

	go h.service.HandleCall(callID, phoneNumber, events.NoopEmitter{})
}

func (h *CallHandler) sendError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(models.ErrorResponse{
		Error: message,
	})
}
