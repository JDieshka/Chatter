package websocket

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"

	pkgauth "github.com/chattergo/chattergo/internal/pkg/auth"
)

// UpgradeHandler handles GET /ws?token={access}: validates JWT at upgrade time,
// then hands the connection to the hub.
func UpgradeHandler(hub *Hub, mgr *pkgauth.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			const hdr = "Authorization"
			if v := r.Header.Get(hdr); v != "" {
				token = trimBearer(v)
			}
		}
		claims, err := mgr.ParseAccessToken(token)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// Per-instance VPS deployment: any origin is allowed.
			InsecureSkipVerify: true,
		})
		if err != nil {
			return // Accept wrote the error
		}
		// Server-side keep-alive pings (writePump equivalent).
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}()
		hub.HandleConn(r.Context(), conn, claims.UserID, claims.Username)
	}
}

func trimBearer(v string) string {
	const p = "Bearer "
	if len(v) > len(p) && v[:len(p)] == p {
		return v[len(p):]
	}
	return v
}
