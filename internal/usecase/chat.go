package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chattergo/chattergo/internal/domain"
)

const maxMessageLen = 4096

// Broadcaster abstracts real-time delivery. In-memory Hub implements it now;
// a Redis Pub/Sub implementation can be swapped in later without touching
// business logic.
type Broadcaster interface {
	BroadcastToChat(chatID string, payload []byte)
	// BroadcastToUsers delivers a payload to all connected clients of the
	// given user IDs (used for chat.created notifications).
	BroadcastToUsers(userIDs []string, payload []byte)
}

type ChatUsecase struct {
	chats       domain.ChatRepository
	users       domain.UserRepository
	msgs        domain.MessageRepository
	reads       domain.ChatReadRepository
	broadcaster Broadcaster
}

func NewChat(chats domain.ChatRepository, users domain.UserRepository, msgs domain.MessageRepository, reads domain.ChatReadRepository, b Broadcaster) *ChatUsecase {
	return &ChatUsecase{chats: chats, users: users, msgs: msgs, reads: reads, broadcaster: b}
}

// MarkRead records that the user has seen all messages of the chat up to now.
// Called when the client opens a chat (REST history request).
func (uc *ChatUsecase) MarkRead(ctx context.Context, chatID, userID string) error {
	if uc.reads == nil {
		return nil
	}
	if err := uc.EnsureMember(ctx, chatID, userID); err != nil {
		return err
	}
	return uc.reads.MarkRead(ctx, chatID, userID)
}

func (uc *ChatUsecase) SetBroadcaster(b Broadcaster) { uc.broadcaster = b }

// GetOrCreatePrivateChat returns an existing private chat for the pair or creates one.
// The second return value reports whether the chat was newly created.
func (uc *ChatUsecase) GetOrCreatePrivateChat(ctx context.Context, userID, peerUsername string) (*domain.Chat, bool, error) {
	peer, err := uc.users.GetByUsername(ctx, strings.TrimSpace(peerUsername))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, false, fmt.Errorf("%w: peer user not found", domain.ErrInvalidInput)
		}
		return nil, false, err
	}
	if peer.ID == userID {
		return nil, false, fmt.Errorf("%w: cannot chat with yourself", domain.ErrInvalidInput)
	}
	chat, err := uc.chats.GetPrivateChat(ctx, userID, peer.ID)
	if err == nil {
		return chat, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, err
	}
	chat, err = uc.chats.CreatePrivateChat(ctx, userID, peer.ID)
	if err != nil {
		return nil, false, err
	}
	// Attach members so clients can render the peer name without another request.
	if ms, err := uc.chats.Members(ctx, chat.ID); err == nil {
		chat.Members = ms
	}
	return chat, true, nil
}

// CreateGroupChat creates a group chat; creator becomes admin.
func (uc *ChatUsecase) CreateGroupChat(ctx context.Context, userID, title string, memberUsernames []string) (*domain.Chat, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 255 {
		return nil, fmt.Errorf("%w: title must be 1-255 characters", domain.ErrInvalidInput)
	}
	members := []domain.ChatMember{{UserID: userID, Role: domain.RoleAdmin}}
	for _, name := range memberUsernames {
		u, err := uc.users.GetByUsername(ctx, strings.TrimSpace(name))
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("%w: user %q not found", domain.ErrInvalidInput, name)
			}
			return nil, err
		}
		if u.ID == userID {
			continue
		}
		members = append(members, domain.ChatMember{UserID: u.ID, Role: domain.RoleMember})
	}
	chat := &domain.Chat{Type: domain.ChatTypeGroup, Title: &title, CreatedBy: userID}
	if err := uc.chats.Create(ctx, chat, members); err != nil {
		return nil, err
	}
	ms, err := uc.chats.Members(ctx, chat.ID)
	if err != nil {
		return nil, err
	}
	chat.Members = ms
	return chat, nil
}

func (uc *ChatUsecase) ListChats(ctx context.Context, userID string) ([]domain.ChatWithLastMessage, error) {
	return uc.chats.ListByUser(ctx, userID)
}

// GetChat returns chat details only to its members.
func (uc *ChatUsecase) GetChat(ctx context.Context, chatID, userID string) (*domain.Chat, error) {
	if err := uc.EnsureMember(ctx, chatID, userID); err != nil {
		return nil, err
	}
	chat, err := uc.chats.GetByID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	chat.Members, err = uc.chats.Members(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return chat, nil
}

// GetChatInfo returns detailed information about a chat (who is in it, who
// created it, when members joined). Accessible only to chat members.
func (uc *ChatUsecase) GetChatInfo(ctx context.Context, chatID, userID string) (*domain.ChatInfo, error) {
	if err := uc.EnsureMember(ctx, chatID, userID); err != nil {
		return nil, err
	}
	chat, err := uc.chats.GetByID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	members, err := uc.chats.MembersWithJoin(ctx, chatID)
	if err != nil {
		return nil, err
	}
	info := &domain.ChatInfo{
		ID: chat.ID, Type: chat.Type, Title: chat.Title,
		CreatedBy: chat.CreatedBy, CreatedAt: chat.CreatedAt,
		Members: make([]domain.MemberInfo, 0, len(members)),
	}
	for _, m := range members {
		isCreator := m.UserID == chat.CreatedBy
		info.Members = append(info.Members, domain.MemberInfo{
			UserID: m.UserID, Username: m.Username, Role: m.Role,
			JoinedAt: m.JoinedAt, IsCreator: isCreator,
		})
	}
	info.MemberCount = len(info.Members)
	if u, err := uc.users.GetByID(ctx, chat.CreatedBy); err == nil {
		u.PasswordHash = ""
		info.Creator = &u
	}
	return info, nil
}

// AddMember adds a user to a group chat. Only group admins may add members.
func (uc *ChatUsecase) AddMember(ctx context.Context, chatID, actorID, username string) error {
	if err := uc.EnsureMember(ctx, chatID, actorID); err != nil {
		return err
	}
	chat, err := uc.chats.GetByID(ctx, chatID)
	if err != nil {
		return err
	}
	if chat.Type != domain.ChatTypeGroup {
		return fmt.Errorf("%w: cannot add members to a private chat", domain.ErrForbidden)
	}
	if err := uc.requireAdmin(ctx, chatID, actorID); err != nil {
		return err
	}
	u, err := uc.users.GetByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: user %q not found", domain.ErrInvalidInput, username)
		}
		return err
	}
	return uc.chats.AddMember(ctx, chatID, domain.ChatMember{UserID: u.ID, Role: domain.RoleMember})
}

func (uc *ChatUsecase) requireAdmin(ctx context.Context, chatID, userID string) error {
	members, err := uc.chats.Members(ctx, chatID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.UserID == userID && m.Role == domain.RoleAdmin {
			return nil
		}
	}
	return fmt.Errorf("%w: chat admin required", domain.ErrForbidden)
}

func (uc *ChatUsecase) EnsureMember(ctx context.Context, chatID, userID string) error {
	ok, err := uc.chats.IsMember(ctx, chatID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return err
	}
	if !ok {
		return domain.ErrForbidden
	}
	return nil
}

// SendMessage persists a message and broadcasts it to chat participants.
func (uc *ChatUsecase) SendMessage(ctx context.Context, chatID, senderID, content string) (*domain.Message, error) {
	content = strings.TrimRight(content, "\r")
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%w: empty message", domain.ErrInvalidInput)
	}
	if len(content) > maxMessageLen {
		return nil, fmt.Errorf("%w: message too long (max %d)", domain.ErrInvalidInput, maxMessageLen)
	}
	if err := uc.EnsureMember(ctx, chatID, senderID); err != nil {
		return nil, err
	}
	sender, err := uc.users.GetByID(ctx, senderID)
	if err != nil {
		return nil, err
	}
	msg := &domain.Message{ChatID: chatID, SenderID: senderID, Content: content}
	if err := uc.msgs.Create(ctx, msg); err != nil {
		return nil, err
	}
	if uc.broadcaster != nil {
		payload := OutgoingMessage{
			Type:      "message.new",
			ChatID:    chatID,
			Sender:    sender,
			Content:   content,
			CreatedAt: msg.CreatedAt,
			ID:        msg.ID,
			SenderID:  senderID,
		}
		uc.broadcaster.BroadcastToChat(chatID, MustJSON(payload))
	}
	return msg, nil
}

// GetMessages returns message history with cursor pagination (before = last loaded id).
func (uc *ChatUsecase) GetMessages(ctx context.Context, chatID, userID string, before int64, limit int) ([]domain.Message, error) {
	if err := uc.EnsureMember(ctx, chatID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return uc.msgs.List(ctx, chatID, before, limit)
}

// MemberIDs exposes chat membership for the WS layer (join validation).
func (uc *ChatUsecase) MemberIDs(ctx context.Context, chatID string) ([]string, error) {
	return uc.chats.MemberIDs(ctx, chatID)
}

// ChatPeerIDs returns user IDs of all members of a chat except the given user.
// Used by the WS layer to notify peers that a new chat was created with them.
func (uc *ChatUsecase) ChatPeerIDs(ctx context.Context, chatID, excludeUserID string) ([]string, error) {
	ids, err := uc.chats.MemberIDs(ctx, chatID)
	if err != nil {
		return nil, err
	}
	peers := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != excludeUserID {
			peers = append(peers, id)
		}
	}
	return peers, nil
}

// NotifyChatCreated broadcasts a chat.created event to every member of the chat
// except the creator, so other clients can show the new chat in real time
// without a page reload.
func (uc *ChatUsecase) NotifyChatCreated(ctx context.Context, chatID, creatorID string, chat *domain.Chat) {
	if uc.broadcaster == nil || chat == nil {
		return
	}
	peers, err := uc.ChatPeerIDs(ctx, chatID, creatorID)
	if err != nil || len(peers) == 0 {
		return
	}
	payload := map[string]interface{}{
		"type":    "chat.created",
		"chat":    chat,
		"chat_id": chatID,
	}
	uc.broadcaster.BroadcastToUsers(peers, MustJSON(payload))
}

func (uc *ChatUsecase) UserByID(ctx context.Context, id string) (*domain.User, error) {
	return uc.users.GetByID(ctx, id)
}
