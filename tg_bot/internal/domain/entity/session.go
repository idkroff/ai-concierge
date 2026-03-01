package entity

type SessionState string

const (
	StateIdle                 SessionState = "idle"
	StateAwaitingConfirmation SessionState = "awaiting_confirmation"
)

type Session struct {
	UserID         int64
	State          SessionState
	PendingMessage string
}
