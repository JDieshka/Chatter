package usecase

import (
	"encoding/json"
	"time"

	"github.com/chattergo/chattergo/internal/domain"
)

// OutgoingMessage is the server->client WS event for a new message.
type OutgoingMessage struct {
	Type      string       `json:"type"`
	ID        int64        `json:"id"`
	ChatID    string       `json:"chat_id"`
	SenderID  string       `json:"sender_id"`
	Sender    *domain.User `json:"sender"`
	Content   string       `json:"content"`
	CreatedAt time.Time    `json:"created_at"`
}

// OutgoingError is the server->client WS error event.
type OutgoingError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func MustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
