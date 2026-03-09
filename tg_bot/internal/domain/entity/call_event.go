package entity

import "encoding/json"

type CallEvent struct {
	Type    string
	CallID  string
	Payload json.RawMessage
}
