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
	PendingPhone   string
	PendingContext string
	Organization   string
}

// ParsedCall — результат /parse: готовый телефон, цель звонка и (опц.) найденная организация.
type ParsedCall struct {
	PhoneNumber  string
	Organization string
	Context      string
	DisplayName  string
	IsHotline    bool
}
