package websocket

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/chattergo/chattergo/internal/usecase"
)

// Client is a single WebSocket connection bound to an authenticated user.
type Client struct {
	conn      *websocket.Conn
	userID    string
	mu        sync.Mutex // guards writes only — a slow client never blocks broadcast
	rooms     map[string]bool
	isClosing bool
	hub       *Hub
}

func (c *Client) send(payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isClosing {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		c.isClosing = true
	}
}

func (c *Client) close(code websocket.StatusCode, reason string) {
	c.mu.Lock()
	if c.isClosing {
		c.mu.Unlock()
		return
	}
	c.isClosing = true
	c.mu.Unlock()
	_ = c.conn.Close(code, reason)
}

// IncomingEvent is a client->server WS message.
type IncomingEvent struct {
	Type    string `json:"type"`
	ChatID  string `json:"chat_id"`
	Content string `json:"content"`
}

// Hub owns all connections; rooms are chat_id -> clients filters over one hub.
// All mutations of the maps happen inside a single goroutine driven by channels,
// so no mutex is needed for them.
type Hub struct {
	// roomsMu guards the fast-path read in BroadcastToChat; all writes to
	// clients/rooms still happen only inside the Run goroutine.
	roomsMu    sync.RWMutex
	clients    map[*Client]bool
	rooms      map[string]map[*Client]bool
	broadcast  chan broadcastItem
	register   chan *Client
	unregister chan *Client
	join       chan roomOp
	leave      chan roomOp
	log        *slog.Logger
	// ChatUC is set after construction to break the hub/usecase dependency cycle.
	ChatUC *usecase.ChatUsecase
}

type broadcastItem struct {
	chatID string
	data   []byte
}

type roomOp struct {
	client *Client
	chatID string
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		rooms:      make(map[string]map[*Client]bool),
		broadcast:  make(chan broadcastItem, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		join:       make(chan roomOp, 64),
		leave:      make(chan roomOp, 64),
		log:        log,
	}
}

// Run is the hub's single goroutine. Stop it by closing ctx.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				c.close(websocket.StatusNormalClosure, "server shutdown")
			}
			return
		case c := <-h.register:
			h.clients[c] = true
			h.log.Info("ws client connected", "user_id", c.userID)
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				for chatID := range c.rooms {
					if r, ok := h.rooms[chatID]; ok {
						delete(r, c)
						if len(r) == 0 {
							delete(h.rooms, chatID)
						}
					}
				}
				c.close(websocket.StatusNormalClosure, "")
				h.log.Info("ws client disconnected", "user_id", c.userID)
			}
		case op := <-h.join:
			h.addRoomMember(op.client, op.chatID)
		case op := <-h.leave:
			if r, ok := h.rooms[op.chatID]; ok {
				delete(r, op.client)
				if len(r) == 0 {
					delete(h.rooms, op.chatID)
				}
			}
			delete(op.client.rooms, op.chatID)
		case item := <-h.broadcast:
			h.deliver(item.chatID, item.data)
		}
	}
}

// addRoomMember subscribes a client to a room. Safe to call from any goroutine.
func (h *Hub) addRoomMember(c *Client, chatID string) {
	h.roomsMu.Lock()
	defer h.roomsMu.Unlock()
	if !h.clients[c] {
		return
	}
	if _, ok := h.rooms[chatID]; !ok {
		h.rooms[chatID] = make(map[*Client]bool)
	}
	h.rooms[chatID][c] = true
	c.rooms[chatID] = true
}

// deliver sends a payload to every client subscribed to the chat's room.
func (h *Hub) deliver(chatID string, data []byte) {
	h.roomsMu.RLock()
	room := h.rooms[chatID]
	targets := make([]*Client, 0, len(room))
	for c := range room {
		targets = append(targets, c)
	}
	h.roomsMu.RUnlock()
	if len(targets) == 0 && h.log != nil {
		h.log.Warn("broadcast dropped: no clients in room", "chat_id", chatID)
	}
	for _, c := range targets {
		c.send(data)
	}
}

// BroadcastToChat implements usecase.Broadcaster (in-memory now, Redis later).
// It delivers synchronously: SendMessage must not return before subscribers
// were notified, otherwise a fast sender could race its own receiver.
func (h *Hub) BroadcastToChat(chatID string, payload []byte) {
	h.deliver(chatID, payload)
}

// Register adds a client to the hub (called after successful upgrade).
func (h *Hub) Register(c *Client) { h.register <- c }

// HandleConn runs readPump for a client; writePump is implicit via send().
func (h *Hub) HandleConn(ctx context.Context, conn *websocket.Conn, userID string) {
	c := &Client{conn: conn, userID: userID, rooms: make(map[string]bool), hub: h}
	h.Register(c)
	defer func() { h.unregister <- c }()

	readTimer := 70 * time.Second
	for {
		wctx, cancel := context.WithTimeout(ctx, readTimer)
		_, data, err := conn.Read(wctx)
		cancel()
		if err != nil {
			return
		}
		// application-level ping/pong keep-alive on top of coder/websocket pings
		var ev IncomingEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: "BAD_JSON", Message: "malformed event"}))
			continue
		}
		h.handleEvent(ctx, c, ev)
	}
}

func (h *Hub) handleEvent(ctx context.Context, c *Client, ev IncomingEvent) {
	switch ev.Type {
	case "chat.join":
		ids, err := h.ChatUC.MemberIDs(ctx, ev.ChatID)
		if err != nil || !contains(ids, c.userID) {
			c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: "CHAT_NOT_FOUND", Message: "chat not found or access denied"}))
			return
		}
		// synchronous subscribe — see comment on BroadcastToChat
		h.addRoomMember(c, ev.ChatID)
	case "chat.leave":
		h.leave <- roomOp{client: c, chatID: ev.ChatID}
	case "message.send":
		if _, err := h.ChatUC.SendMessage(ctx, ev.ChatID, c.userID, ev.Content); err != nil {
			code := "INTERNAL"
			if err.Error() != "" {
				code = "SEND_FAILED"
			}
			c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: code, Message: err.Error()}))
		}
	case "ping":
		c.send([]byte(`{"type":"pong"}`))
	default:
		c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: "UNKNOWN_TYPE", Message: "unknown event type: " + ev.Type}))
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"type":"error","code":"INTERNAL","message":"marshal failed"}`)
	}
	return b
}
