package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/chattergo/chattergo/internal/domain"
	pkgauth "github.com/chattergo/chattergo/internal/pkg/auth"
	"github.com/chattergo/chattergo/internal/pkg/middleware"
	"github.com/chattergo/chattergo/internal/usecase"
)

type Handler struct {
	auth *usecase.AuthUsecase
	chat *usecase.ChatUsecase
	jwt  *pkgauth.Manager
	log  *slog.Logger
}

func NewHandler(auth *usecase.AuthUsecase, chat *usecase.ChatUsecase, jwt *pkgauth.Manager, log *slog.Logger) *Handler {
	return &Handler{auth: auth, chat: chat, jwt: jwt, log: log}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// respondErr maps domain errors to HTTP statuses.
func (h *Handler) respondErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrInvalidCreds), errors.Is(err, domain.ErrTokenExpired):
		writeErr(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, domain.ErrForbidden):
		writeErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	default:
		h.log.Error("internal error", "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal server error")
	}
}

func decode(r *http.Request, dst interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return domain.ErrInvalidInput
	}
	return json.Unmarshal(body, dst)
}

// RegisterRoutes mounts all REST endpoints.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/api", func(api chi.Router) {
		api.Post("/auth/register", h.register)
		api.Post("/auth/login", h.login)
		api.Post("/auth/refresh", h.refresh)

		api.Group(func(priv chi.Router) {
			priv.Use(middleware.JWT(h.jwt))
			priv.Get("/users/me", h.me)
			priv.Get("/chats", h.listChats)
			priv.Post("/chats/private", h.createPrivateChat)
			priv.Post("/chats/group", h.createGroupChat)
			priv.Route("/chats/{id}", func(c chi.Router) {
				c.Get("/", h.getChat)
				c.Get("/info", h.getChatInfo)
				c.Post("/members", h.addMember)
				c.Get("/messages", h.getMessages)
				c.Post("/messages", h.sendMessage)
			})
		})
	})
}

type authReq struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokensResp struct {
	User         *domain.User `json:"user,omitempty"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int          `json:"expires_in"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if err := decode(r, &req); err != nil {
		h.respondErr(w, r, domain.ErrInvalidInput)
		return
	}
	u, pair, err := h.auth.Register(r.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, tokensResp{User: u, AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, ExpiresIn: int(h.jwt.AccessTTL().Seconds())})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if err := decode(r, &req); err != nil {
		h.respondErr(w, r, domain.ErrInvalidInput)
		return
	}
	u, pair, err := h.auth.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokensResp{User: u, AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, ExpiresIn: int(h.jwt.AccessTTL().Seconds())})
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decode(r, &req); err != nil || req.RefreshToken == "" {
		h.respondErr(w, r, domain.ErrInvalidInput)
		return
	}
	pair, err := h.auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokensResp{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, ExpiresIn: int(h.jwt.AccessTTL().Seconds())})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	u, err := h.auth.Me(r.Context(), claims.UserID)
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func currentUserID(r *http.Request) string {
	if c := middleware.ClaimsFrom(r.Context()); c != nil {
		return c.UserID
	}
	return ""
}

func (h *Handler) listChats(w http.ResponseWriter, r *http.Request) {
	chats, err := h.chat.ListChats(r.Context(), currentUserID(r))
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	if chats == nil {
		chats = []domain.ChatWithLastMessage{}
	}
	writeJSON(w, http.StatusOK, chats)
}

func (h *Handler) createPrivateChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerUsername string `json:"peer_username"`
	}
	if err := decode(r, &req); err != nil {
		h.respondErr(w, r, domain.ErrInvalidInput)
		return
	}
	chat, created, err := h.chat.GetOrCreatePrivateChat(r.Context(), currentUserID(r), req.PeerUsername)
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	if created {
		// Notify the peer's other sessions in real time about the new chat.
		h.chat.NotifyChatCreated(r.Context(), chat.ID, currentUserID(r), chat)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, chat)
}

func (h *Handler) createGroupChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title           string   `json:"title"`
		MemberUsernames []string `json:"member_usernames"`
	}
	if err := decode(r, &req); err != nil {
		h.respondErr(w, r, domain.ErrInvalidInput)
		return
	}
	chat, err := h.chat.CreateGroupChat(r.Context(), currentUserID(r), req.Title, req.MemberUsernames)
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	// Notify invited members' other sessions in real time about the new group.
	h.chat.NotifyChatCreated(r.Context(), chat.ID, currentUserID(r), chat)
	writeJSON(w, http.StatusCreated, chat)
}

func (h *Handler) getChat(w http.ResponseWriter, r *http.Request) {
	chat, err := h.chat.GetChat(r.Context(), chi.URLParam(r, "id"), currentUserID(r))
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, chat)
}

func (h *Handler) getChatInfo(w http.ResponseWriter, r *http.Request) {
	info, err := h.chat.GetChatInfo(r.Context(), chi.URLParam(r, "id"), currentUserID(r))
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
	}
	if err := decode(r, &req); err != nil {
		h.respondErr(w, r, domain.ErrInvalidInput)
		return
	}
	if err := h.chat.AddMember(r.Context(), chi.URLParam(r, "id"), currentUserID(r), req.Username); err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) getMessages(w http.ResponseWriter, r *http.Request) {
	before := int64(0)
	if v := r.URL.Query().Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			h.respondErr(w, r, domain.ErrInvalidInput)
			return
		}
		before = n
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			h.respondErr(w, r, domain.ErrInvalidInput)
			return
		}
		limit = n
	}
	msgs, err := h.chat.GetMessages(r.Context(), chi.URLParam(r, "id"), currentUserID(r), before, limit)
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	// Opening history (first page) counts as reading the chat: reset unread counter.
	if before == 0 {
		_ = h.chat.MarkRead(r.Context(), chi.URLParam(r, "id"), currentUserID(r))
	}
	if msgs == nil {
		msgs = []domain.Message{}
	}
	// Единый формат ответа с курсором пагинации:
	// next_before — id самого раннего сообщения в выборке (0 если история пуста),
	// has_more — признак того, что страница заполнена полностью и старше ещё есть сообщения.
	nextBefore := int64(0)
	if len(msgs) > 0 {
		nextBefore = msgs[0].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"messages":    msgs,
		"next_before": nextBefore,
		"has_more":    len(msgs) >= limit,
	})
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := decode(r, &req); err != nil {
		h.respondErr(w, r, domain.ErrInvalidInput)
		return
	}
	msg, err := h.chat.SendMessage(r.Context(), chi.URLParam(r, "id"), currentUserID(r), strings.TrimSpace(req.Content))
	if err != nil {
		h.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}
