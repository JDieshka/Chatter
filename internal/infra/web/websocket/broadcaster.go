package websocket

import (
	"sync"

	"github.com/chattergo/chattergo/internal/usecase"
)

// MultiBroadcaster implements usecase.Broadcaster and fans a payload out to
// every registered hub. This lets the same process serve both /ws (text chats)
// and, in the future, a separate voice-signalling hub without changing the
// usecase layer: each hub is just another subscriber.
type MultiBroadcaster struct {
	mu   sync.RWMutex
	hubs []usecase.Broadcaster
}

func NewMultiBroadcaster(hubs ...usecase.Broadcaster) *MultiBroadcaster {
	return &MultiBroadcaster{hubs: hubs}
}

func (m *MultiBroadcaster) Add(h usecase.Broadcaster) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hubs = append(m.hubs, h)
}

func (m *MultiBroadcaster) BroadcastToChat(chatID string, payload []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, h := range m.hubs {
		h.BroadcastToChat(chatID, payload)
	}
}

func (m *MultiBroadcaster) BroadcastToUsers(userIDs []string, payload []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, h := range m.hubs {
		h.BroadcastToUsers(userIDs, payload)
	}
}
