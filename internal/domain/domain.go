package domain

import (
	"context"
	"errors"
	"time"
)

// Chat types.
const (
	ChatTypePrivate = "private"
	ChatTypeGroup   = "group"
)

// Member roles.
const (
	RoleMember = "member"
	RoleAdmin  = "admin"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrForbidden     = errors.New("forbidden: not a chat member")
	ErrInvalidInput  = errors.New("invalid input")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrInvalidCreds  = errors.New("invalid username or password")
	ErrTokenExpired  = errors.New("token expired or invalid")
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Chat struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Title     *string         `json:"title"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	Members   []ChatMemberDTO `json:"members,omitempty"`
}

// ChatMemberDTO is a projection of membership joined with user info,
// safe to expose over the API.
type ChatMemberDTO struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// MemberInfo extends ChatMemberDTO with join date and creator flag for the
// chat-info view.
type MemberInfo struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joined_at"`
	IsCreator bool      `json:"is_creator"`
}

type ChatMember struct {
	ChatID   string
	UserID   string
	Role     string
	JoinedAt time.Time
}

type Message struct {
	ID        int64     `json:"id"`
	ChatID    string    `json:"chat_id"`
	SenderID  string    `json:"sender_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatWithLastMessage enriches a chat for the chat list view.
type ChatWithLastMessage struct {
	Chat
	LastMessageAt *time.Time `json:"last_message_at"`
	// LastContent is the text of the most recent message (preview in UI).
	LastContent string `json:"last_content,omitempty"`
	// UnreadCount is the number of messages since the user last opened the
	// chat (0 when open or no history).
	UnreadCount int64 `json:"unread_count"`
}

// ChatReadRepository tracks per-user read position in chats.
type ChatReadRepository interface {
	MarkRead(ctx context.Context, chatID, userID string) error
}

// Repository interfaces (gateways). Infra implements them.

type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
}

type ChatRepository interface {
	Create(ctx context.Context, c *Chat, members []ChatMember) error
	GetByID(ctx context.Context, id string) (*Chat, error)
	ListByUser(ctx context.Context, userID string) ([]ChatWithLastMessage, error)
	GetPrivateChat(ctx context.Context, userA, userB string) (*Chat, error)
	CreatePrivateChat(ctx context.Context, userA, userB string) (*Chat, error)
	AddMember(ctx context.Context, chatID string, m ChatMember) error
	IsMember(ctx context.Context, chatID, userID string) (bool, error)
	Members(ctx context.Context, chatID string) ([]ChatMemberDTO, error)
	MembersWithJoin(ctx context.Context, chatID string) ([]ChatMemberWithJoin, error)
	MemberIDs(ctx context.Context, chatID string) ([]string, error)
}

// ChatMemberWithJoin is a membership row joined with user info and join date.
type ChatMemberWithJoin struct {
	UserID   string
	Username string
	Role     string
	JoinedAt time.Time
}

// ChatInfo is the full chat card shown in the UI: metadata plus members with
// their roles, join dates and creator flag.
type ChatInfo struct {
	ID          string       `json:"id"`
	Type        string       `json:"type"`
	Title       *string      `json:"title"`
	CreatedBy   string       `json:"created_by"`
	Creator     *User        `json:"creator,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	MemberCount int          `json:"member_count"`
	Members     []MemberInfo `json:"members"`
}

type MessageRepository interface {
	Create(ctx context.Context, m *Message) error
	// List returns up to limit messages of a chat ordered ascending by id.
	// If before > 0, only messages with id < before are considered.
	List(ctx context.Context, chatID string, before int64, limit int) ([]Message, error)
}

type RefreshTokenRepository interface {
	// Store saves SHA-256 hash of the raw token bound to a user.
	Store(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error
	GetUserID(ctx context.Context, tokenHash string) (string, error)
	Delete(ctx context.Context, tokenHash string) error
}
