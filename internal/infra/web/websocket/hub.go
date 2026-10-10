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
	conn       *websocket.Conn
	userID     string
	username   string
	mu         sync.Mutex // guards writes only — a slow client never blocks broadcast
	rooms      map[string]bool
	isClosing  bool
	hub        *Hub
	voiceRooms map[string]bool // voice rooms the client is actively connected to (audio)
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
	Type         string          `json:"type"`
	ChatID       string          `json:"chat_id"`
	Content      string          `json:"content"`
	Payload      json.RawMessage `json:"payload"`        // for voice.* signalling events
	TargetUserID string          `json:"target_user_id"` // recipient of a voice.* relay
}

// OutgoingSignal is a server->client relay event (voice signalling, presence).
// It also carries the target user for point-to-point relays: the browser-side
// router checks it and ignores signals addressed to other peers.
type OutgoingSignal struct {
	Type     string          `json:"type"`
	ChatID   string          `json:"chat_id,omitempty"`
	UserID   string          `json:"user_id,omitempty"`  // the peer this signal came from
	Username string          `json:"username,omitempty"` // display name of that peer
	Action   string          `json:"action,omitempty"`   // join|leave
	SenderID string          `json:"sender_id,omitempty"`
	Target   string          `json:"target,omitempty"` // intended recipient user id
	Payload  json.RawMessage `json:"payload,omitempty"`
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
	registered chan regItem
	unregister chan *Client
	join       chan roomOp
	leave      chan roomOp
	log        *slog.Logger
	// ChatUC is set after construction to break the hub/usecase dependency cycle.
	ChatUC *usecase.ChatUsecase
}

type regItem struct {
	client *Client
	done   chan<- struct{}
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
		registered: make(chan regItem),
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
		case ri := <-h.registered:
			if _, ok := h.clients[ri.client]; !ok {
				h.clients[ri.client] = true
			}
			if ri.done != nil {
				close(ri.done)
			}
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

// BroadcastToUsers implements usecase.Broadcaster: delivers a payload to every
// connected client of the listed users, regardless of room subscriptions.
func (h *Hub) BroadcastToUsers(userIDs []string, payload []byte) {
	want := make(map[string]bool, len(userIDs))
	for _, id := range userIDs {
		want[id] = true
	}
	h.roomsMu.RLock()
	targets := make([]*Client, 0)
	for c := range h.clients {
		if want[c.userID] {
			targets = append(targets, c)
		}
	}
	h.roomsMu.RUnlock()
	for _, c := range targets {
		c.send(payload)
	}
}

// autoJoinRooms subscribes a freshly connected client to all chats they are a
// member of, so messages in any chat arrive without an explicit chat.join.
func (h *Hub) autoJoinRooms(ctx context.Context, c *Client) {
	if h.ChatUC == nil {
		return
	}
	list, err := h.ChatUC.ListChats(ctx, c.userID)
	if err != nil {
		h.log.Warn("ws auto-join failed", "user_id", c.userID, "err", err)
		return
	}
	for _, ch := range list {
		h.addRoomMember(c, ch.ID)
	}
}

// Register adds a client to the hub (called after successful upgrade).
func (h *Hub) Register(c *Client) { h.register <- c }

// AwaitRegistered blocks until the Run goroutine has processed this client's
// registration. Without it, autoJoinRooms/broadcast could race the map update.
func (h *Hub) AwaitRegistered(c *Client, done chan<- struct{}) {
	h.registered <- regItem{client: c, done: done}
}

// HandleConn runs readPump for a client; writePump is implicit via send().
func (h *Hub) HandleConn(ctx context.Context, conn *websocket.Conn, userID, username string) {
	c := &Client{conn: conn, userID: userID, username: username, rooms: make(map[string]bool), voiceRooms: make(map[string]bool), hub: h}
	h.Register(c)
	// Ensure the hub goroutine actually added us before we subscribe to rooms.
	regDone := make(chan struct{})
	h.AwaitRegistered(c, regDone)
	select {
	case <-regDone:
	case <-time.After(5 * time.Second):
	}
	// Subscribe to all chats of this user so messages arrive in real time even
	// if the client hasn't sent chat.join yet (fixes new-chat first-message race).
	h.autoJoinRooms(ctx, c)
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
	case "voice.join":
		h.voiceJoin(ctx, c, ev.ChatID)
	case "voice.leave":
		h.voiceLeave(c, ev.ChatID)
	case "voice.offer", "voice.answer", "voice.ice":
		// Pure signalling relay: SDP/ICE goes to one peer in the same voice room.
		c.mu.Lock()
		joined := c.voiceRooms[ev.ChatID]
		c.mu.Unlock()
		if !joined {
			h.voiceJoin(ctx, c, ev.ChatID) // auto-join so relays work after reconnect
			c.mu.Lock()
			joined = c.voiceRooms[ev.ChatID]
			c.mu.Unlock()
			if !joined {
				return // access denied — error already sent by voiceJoin
			}
		}
		if ev.TargetUserID == "" || len(ev.Payload) == 0 {
			c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: "BAD_SIGNAL", Message: "target_user_id and payload are required"}))
			return
		}
		h.deliver(ev.ChatID, mustMarshal(OutgoingSignal{
			Type:     ev.Type,
			ChatID:   ev.ChatID,
			UserID:   c.userID,
			Username: c.username,
			SenderID: c.userID,
			Target:   ev.TargetUserID,
			Payload:  ev.Payload,
		}))
	default:
		c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: "UNKNOWN_TYPE", Message: "unknown event type: " + ev.Type}))
	}
}

// voiceJoin subscribes the client to a voice room's signalling channel and
// notifies other connected participants (mesh WebRTC topology, MVP).
func (h *Hub) voiceJoin(ctx context.Context, c *Client, chatID string) {
	if chatID == "" {
		c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: "BAD_JSON", Message: "chat_id is required"}))
		return
	}
	ids, err := h.ChatUC.MemberIDs(ctx, chatID)
	if err != nil || !contains(ids, c.userID) {
		c.send(mustMarshal(usecase.OutgoingError{Type: "error", Code: "CHAT_NOT_FOUND", Message: "chat not found or access denied"}))
		return
	}
	h.addRoomMember(c, chatID)
	c.mu.Lock()
	c.voiceRooms[chatID] = true
	c.mu.Unlock()
	// Tell existing peers a new participant arrived — they will initiate offers.
	h.deliverExcept(chatID, mustMarshal(OutgoingSignal{
		Type: "voice.peer.joined", ChatID: chatID, UserID: c.userID, Username: c.username, Action: "join",
	}), c)
}

// voiceLeave unsubscribes from voice presence and notifies remaining peers.
func (h *Hub) voiceLeave(c *Client, chatID string) {
	c.mu.Lock()
	joined := c.voiceRooms[chatID]
	delete(c.voiceRooms, chatID)
	c.mu.Unlock()
	if !joined {
		return
	}
	h.deliver(chatID, mustMarshal(OutgoingSignal{
		Type: "voice.peer.left", ChatID: chatID, UserID: c.userID, Username: c.username, Action: "leave",
	}))
}

// deliverExcept sends a payload to every subscriber of the room except `skip`.
func (h *Hub) deliverExcept(chatID string, data []byte, skip *Client) {
	h.roomsMu.RLock()
	room := h.rooms[chatID]
	targets := make([]*Client, 0, len(room))
	for cl := range room {
		if cl != skip {
			targets = append(targets, cl)
		}
	}
	h.roomsMu.RUnlock()
	for _, cl := range targets {
		cl.send(data)
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
