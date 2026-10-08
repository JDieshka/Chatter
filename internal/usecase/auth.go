package usecase

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/chattergo/chattergo/internal/domain"
	pkgauth "github.com/chattergo/chattergo/internal/pkg/auth"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,64}$`)

type AuthUsecase struct {
	users    domain.UserRepository
	tokens   domain.RefreshTokenRepository
	jwt      *pkgauth.Manager
}

func NewAuth(users domain.UserRepository, tokens domain.RefreshTokenRepository, jwt *pkgauth.Manager) *AuthUsecase {
	return &AuthUsecase{users: users, tokens: tokens, jwt: jwt}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (uc *AuthUsecase) Register(ctx context.Context, username, email, password string) (*domain.User, *pkgauth.TokenPair, error) {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return nil, nil, fmt.Errorf("%w: username must be 3-64 chars of letters, digits, _ . -", domain.ErrInvalidInput)
	}
	if len(password) < 6 {
		return nil, nil, fmt.Errorf("%w: password must be at least 6 characters", domain.ErrInvalidInput)
	}
	hash, err := pkgauth.HashPassword(password)
	if err != nil {
		return nil, nil, err
	}
	u := &domain.User{Username: username, PasswordHash: hash}
	if email = strings.TrimSpace(email); email != "" {
		u.Email = email
	}
	if err := uc.users.Create(ctx, u); err != nil {
		if isUniqueViolation(err) {
			return nil, nil, fmt.Errorf("%w: username or email already taken", domain.ErrAlreadyExists)
		}
		return nil, nil, err
	}
	pair, err := uc.issueWithRefresh(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	return u, pair, nil
}

func (uc *AuthUsecase) Login(ctx context.Context, username, password string) (*domain.User, *pkgauth.TokenPair, error) {
	u, err := uc.users.GetByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil, domain.ErrInvalidCreds
		}
		return nil, nil, err
	}
	if !pkgauth.CheckPassword(u.PasswordHash, password) {
		return nil, nil, domain.ErrInvalidCreds
	}
	pair, err := uc.issueWithRefresh(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	return u, pair, nil
}

// Refresh rotates the refresh token: old one is invalidated, new pair issued.
func (uc *AuthUsecase) Refresh(ctx context.Context, rawRefresh string) (*pkgauth.TokenPair, error) {
	oldHash := pkgauth.HashToken(rawRefresh)
	userID, err := uc.tokens.GetUserID(ctx, oldHash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrTokenExpired
		}
		return nil, err
	}
	u, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := uc.tokens.Delete(ctx, oldHash); err != nil {
		return nil, err
	}
	pair, err := uc.issueWithRefresh(ctx, u)
	if err != nil {
		return nil, err
	}
	return pair, nil
}

func (uc *AuthUsecase) Me(ctx context.Context, userID string) (*domain.User, error) {
	return uc.users.GetByID(ctx, userID)
}

func (uc *AuthUsecase) issueWithRefresh(ctx context.Context, u *domain.User) (*pkgauth.TokenPair, error) {
	pair, err := uc.jwt.IssueTokens(u.ID, u.Username)
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(uc.jwt.RefreshTTL())
	if err := uc.tokens.Store(ctx, pkgauth.HashToken(pair.RefreshToken), u.ID, expires); err != nil {
		return nil, err
	}
	return pair, nil
}
