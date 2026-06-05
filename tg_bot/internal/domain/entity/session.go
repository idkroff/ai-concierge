package entity

type SessionState string

const (
	StateIdle                 SessionState = "idle"
	StateAwaitingConfirmation SessionState = "awaiting_confirmation"
)

type Session struct {
	UserID         int64
	State          SessionState
	PendingMessage string // сырое сообщение пользователя (для справки)
	PendingPhone   string // нормализованный 11-значный номер (после резолва)
	PendingContext string // цель звонка без номера/названия
	Organization   string // название организации, если номер найден по нему
}

// ParsedCall — результат разбора сообщения caller-сервисом (preview /parse):
// телефон (готовый к набору), цель звонка и, опционально, название организации,
// по которому номер был найден.
type ParsedCall struct {
	PhoneNumber  string
	Organization string
	Context      string
	DisplayName  string // описание найденной точки (название + адрес), если резолвили по организации
	IsHotline    bool   // номер похож на федеральную горячую линию (8-800)
}
