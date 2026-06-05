package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"concierge/internal/models"
	"concierge/internal/parser"
)

// ParseHandler — preview /parse: разбор сообщения и резолв номера без старта звонка.
type ParseHandler struct {
	parser *parser.Parser
}

func NewParseHandler(p *parser.Parser) *ParseHandler {
	return &ParseHandler{parser: p}
}

type parseRequest struct {
	Text string `json:"text"`
}

type parseResponse struct {
	PhoneNumber  string `json:"phone_number"`
	Organization string `json:"organization"`
	Context      string `json:"context"`
	DisplayName  string `json:"display_name"`
	IsHotline    bool   `json:"is_hotline"`
}

func (h *ParseHandler) HandleParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendError(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	var req parseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		h.sendError(w, "Параметр text обязателен", http.StatusBadRequest)
		return
	}

	// Резолв организации может включать веб-поиск + LLM, поэтому таймаут больше парсинга.
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	parsed, err := h.parser.Parse(ctx, req.Text)
	if err != nil {
		log.Printf("[parse] %v", err)
		h.sendError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(parseResponse{
		PhoneNumber:  parsed.PhoneNumber,
		Organization: parsed.Organization,
		Context:      parsed.Context,
		DisplayName:  parsed.DisplayName,
		IsHotline:    parsed.IsHotline,
	})
}

func (h *ParseHandler) sendError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(models.ErrorResponse{Error: message})
}
