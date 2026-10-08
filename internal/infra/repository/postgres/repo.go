package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chattergo/chattergo/internal/domain"
)

func mapNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

type Repo struct {
	pool *pgxpool.Pool
}

func New(dsn string) (*Repo, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("connect pool: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Repo{pool: pool}, nil
}

func (r *Repo) Close() { r.pool.Close() }
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

// ---- Users ----

type UserRepo struct{ r *Repo }

func NewUserRepo(r *Repo) *UserRepo { return &UserRepo{r: r} }

func (u *UserRepo) Create(ctx context.Context, usr *domain.User) error {
	return u.r.pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, NULLIF($2,''), $3)
		 RETURNING id, created_at, updated_at`,
		usr.Username, usr.Email, usr.PasswordHash,
	).Scan(&usr.ID, &usr.CreatedAt, &usr.UpdatedAt)
}

func (u *UserRepo) getOne(ctx context.Context, query string, args ...interface{}) (*domain.User, error) {
	usr := &domain.User{}
	err := u.r.pool.QueryRow(ctx, query, args...).Scan(
		&usr.ID, &usr.Username, &usr.Email, &usr.PasswordHash, &usr.CreatedAt, &usr.UpdatedAt)
	if err != nil {
		return nil, mapNoRows(err)
	}
	return usr, nil
}

func (u *UserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return u.getOne(ctx,
		`SELECT id, username, COALESCE(email,''), password_hash, created_at, updated_at FROM users WHERE id = $1`, id)
}

func (u *UserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return u.getOne(ctx,
		`SELECT id, username, COALESCE(email,''), password_hash, created_at, updated_at FROM users WHERE username = $1`, username)
}

// ---- Chats ----

type ChatRepo struct{ r *Repo }

func NewChatRepo(r *Repo) *ChatRepo { return &ChatRepo{r: r} }

const chatCols = `id, type, title, created_by::text, created_at`

func scanChat(row interface{ Scan(...interface{}) error }) (*domain.Chat, error) {
	c := &domain.Chat{}
	if err := row.Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.CreatedAt); err != nil {
		return nil, mapNoRows(err)
	}
	return c, nil
}

func (c *ChatRepo) Create(ctx context.Context, chat *domain.Chat, members []domain.ChatMember) error {
	tx, err := c.r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx,
		`INSERT INTO chats (type, title, created_by) VALUES ($1, $2, $3) RETURNING id, created_at`,
		chat.Type, chat.Title, chat.CreatedBy).Scan(&chat.ID, &chat.CreatedAt)
	if err != nil {
		return err
	}
	for _, m := range members {
		if _, err := tx.Exec(ctx,
			`INSERT INTO chat_members (chat_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			chat.ID, m.UserID, m.Role); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (c *ChatRepo) GetByID(ctx context.Context, id string) (*domain.Chat, error) {
	return scanChat(c.r.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM chats WHERE id = $1`, chatCols), id))
}

func (c *ChatRepo) ListByUser(ctx context.Context, userID string) ([]domain.ChatWithLastMessage, error) {
	rows, err := c.r.pool.Query(ctx, `
		SELECT ch.id, ch.type, ch.title, ch.created_by::text, ch.created_at,
		       (SELECT MAX(created_at) FROM messages m WHERE m.chat_id = ch.id) AS last_msg
		FROM chats ch
		JOIN chat_members cm ON cm.chat_id = ch.id AND cm.user_id = $1
		ORDER BY last_msg DESC NULLS LAST, ch.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ChatWithLastMessage
	for rows.Next() {
		var cw domain.ChatWithLastMessage
		if err := rows.Scan(&cw.ID, &cw.Type, &cw.Title, &cw.CreatedBy, &cw.CreatedAt, &cw.LastMessageAt); err != nil {
			return nil, err
		}
		out = append(out, cw)
	}
	// attach members
	for i := range out {
		members, err := c.Members(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Members = members
	}
	return out, rows.Err()
}

func (c *ChatRepo) GetPrivateChat(ctx context.Context, userA, userB string) (*domain.Chat, error) {
	chat, err := scanChat(c.r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM chats ch
		WHERE ch.type = 'private'
		  AND EXISTS (SELECT 1 FROM chat_members a WHERE a.chat_id = ch.id AND a.user_id = $1)
		  AND EXISTS (SELECT 1 FROM chat_members b WHERE b.chat_id = ch.id AND b.user_id = $2)
		  AND (SELECT COUNT(*) FROM chat_members m WHERE m.chat_id = ch.id) = 2`, chatCols),
		userA, userB))
	if err != nil {
		return nil, err
	}
	chat.Members, err = c.Members(ctx, chat.ID)
	if err != nil {
		return nil, err
	}
	return chat, nil
}

func (c *ChatRepo) CreatePrivateChat(ctx context.Context, userA, userB string) (*domain.Chat, error) {
	createdBy := userA
	chat := &domain.Chat{Type: domain.ChatTypePrivate, CreatedBy: createdBy}
	members := []domain.ChatMember{
		{UserID: userA, Role: domain.RoleMember},
		{UserID: userB, Role: domain.RoleMember},
	}
	if err := c.Create(ctx, chat, members); err != nil {
		// On race, another instance may have created the pair's chat already.
		if existing, gerr := c.GetPrivateChat(ctx, userA, userB); gerr == nil {
			return existing, nil
		}
		return nil, err
	}
	chat.Members, _ = c.Members(ctx, chat.ID)
	return chat, nil
}

func (c *ChatRepo) AddMember(ctx context.Context, chatID string, m domain.ChatMember) error {
	_, err := c.r.pool.Exec(ctx,
		`INSERT INTO chat_members (chat_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		chatID, m.UserID, m.Role)
	return err
}

func (c *ChatRepo) IsMember(ctx context.Context, chatID, userID string) (bool, error) {
	if _, err := c.GetByID(ctx, chatID); err != nil {
		return false, err
	}
	var ok bool
	err := c.r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM chat_members WHERE chat_id = $1 AND user_id = $2)`,
		chatID, userID).Scan(&ok)
	return ok, err
}

func (c *ChatRepo) Members(ctx context.Context, chatID string) ([]domain.ChatMemberDTO, error) {
	rows, err := c.r.pool.Query(ctx, `
		SELECT cm.user_id::text, u.username, cm.role
		FROM chat_members cm JOIN users u ON u.id = cm.user_id
		WHERE cm.chat_id = $1 ORDER BY cm.joined_at`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ChatMemberDTO
	for rows.Next() {
		var m domain.ChatMemberDTO
		if err := rows.Scan(&m.UserID, &m.Username, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (c *ChatRepo) MemberIDs(ctx context.Context, chatID string) ([]string, error) {
	rows, err := c.r.pool.Query(ctx, `SELECT user_id::text FROM chat_members WHERE chat_id = $1`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- Messages ----

type MessageRepo struct{ r *Repo }

func NewMessageRepo(r *Repo) *MessageRepo { return &MessageRepo{r: r} }

func (m *MessageRepo) Create(ctx context.Context, msg *domain.Message) error {
	return m.r.pool.QueryRow(ctx,
		`INSERT INTO messages (chat_id, sender_id, content) VALUES ($1, $2, $3) RETURNING id, created_at`,
		msg.ChatID, msg.SenderID, msg.Content).Scan(&msg.ID, &msg.CreatedAt)
}

func (m *MessageRepo) List(ctx context.Context, chatID string, before int64, limit int) ([]domain.Message, error) {
	var rows pgx.Rows
	var err error
	if before > 0 {
		rows, err = m.r.pool.Query(ctx, `
			SELECT id, chat_id::text, sender_id::text, content, created_at
			FROM messages WHERE chat_id = $1 AND id < $2
			ORDER BY id DESC LIMIT $3`, chatID, before, limit)
	} else {
		rows, err = m.r.pool.Query(ctx, `
			SELECT id, chat_id::text, sender_id::text, content, created_at
			FROM messages WHERE chat_id = $1
			ORDER BY id DESC LIMIT $2`, chatID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Message
	for rows.Next() {
		var msg domain.Message
		if err := rows.Scan(&msg.ID, &msg.ChatID, &msg.SenderID, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to ascending order
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ---- Refresh tokens ----

type RefreshTokenRepo struct{ r *Repo }

func NewRefreshTokenRepo(r *Repo) *RefreshTokenRepo { return &RefreshTokenRepo{r: r} }

func (t *RefreshTokenRepo) Store(ctx context.Context, tokenHash, userID string, expiresAt time.Time) error {
	_, err := t.r.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	return err
}

func (t *RefreshTokenRepo) GetUserID(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	err := t.r.pool.QueryRow(ctx,
		`SELECT user_id::text FROM refresh_tokens WHERE token_hash = $1 AND expires_at > NOW()`,
		tokenHash).Scan(&userID)
	if err != nil {
		return "", mapNoRows(err)
	}
	return userID, nil
}

func (t *RefreshTokenRepo) Delete(ctx context.Context, tokenHash string) error {
	_, err := t.r.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE token_hash = $1`, tokenHash)
	return err
}
