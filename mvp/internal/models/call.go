package models

type CallStartRequest struct {
	PhoneNumber string `json:"phone_number"`
}

type CallStartResponse struct {
	Status string `json:"status"`
	CallID string `json:"call_id"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

