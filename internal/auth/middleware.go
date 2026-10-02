package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

const (
	SessionCookie = "ecogo_session"
	CSRFCookie    = "ecogo_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

// Principal is the signed-in user and the tenant they are working in.
type Principal struct {
	SessionID uuid.UUID
	UserID    uuid.UUID
	TenantID  uuid.UUID // uuid.Nil when the user has no tenant selected
	Role      dbq.MemberRole
}

type principalKey struct{}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// WithPrincipal returns ctx carrying p; used by tests and by RequireSession.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// RequireSession resolves the session cookie, slides its idle expiry, and rejects anonymous requests.
func (s *Service) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil || c.Value == "" {
			httpx.WriteError(w, r, s.log, httpx.ErrUnauthorized)
			return
		}
		var p Principal
		err = s.db.Global(r.Context(), func(q *dbq.Queries, tx pgx.Tx) error {
			sess, err := q.GetSessionByTokenHash(r.Context(), hashToken(c.Value))
			if err != nil {
				return err
			}
			p = Principal{SessionID: sess.ID, UserID: sess.UserID}
			if sess.TenantID != nil {
				// The role is read through user_memberships so a removed member loses access at once.
				ms, err := q.UserMemberships(r.Context(), sess.UserID)
				if err != nil {
					return err
				}
				for _, m := range ms {
					if m.TenantID == *sess.TenantID && m.TenantStatus == dbq.TenantStatusActive {
						p.TenantID, p.Role = m.TenantID, m.Role
					}
				}
			}
			if time.Since(sess.LastSeenAt) > time.Minute {
				return q.TouchSession(r.Context(), dbq.TouchSessionParams{ID: sess.ID, ExpiresAt: time.Now().Add(s.cfg.SessionTTL)})
			}
			return nil
		})
		if db.IsNotFound(err) {
			s.clearSessionCookie(w)
			httpx.WriteError(w, r, s.log, httpx.ErrUnauthorized)
			return
		}
		if err != nil {
			httpx.WriteError(w, r, s.log, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// RequireTenant rejects requests from a user with no active tenant selected.
func RequireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFrom(r.Context()); !ok || p.TenantID == uuid.Nil {
			httpx.JSON(w, http.StatusForbidden, map[string]any{"error": httpx.Error{
				Code: "forbidden", Message: "Select a workspace first.", RequestID: httpx.GetRequestID(r.Context())}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole allows only the listed roles in the current tenant.
func RequireRole(roles ...dbq.MemberRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := PrincipalFrom(r.Context())
			for _, role := range roles {
				if p.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.JSON(w, http.StatusForbidden, map[string]any{"error": httpx.Error{
				Code: "forbidden", Message: "Your role cannot do this.", RequestID: httpx.GetRequestID(r.Context())}})
		})
	}
}

// CSRF implements the double-submit cookie pattern for the dashboard's /internal endpoints:
// every response makes sure a readable CSRF cookie exists, and every unsafe request must echo it
// in the X-CSRF-Token header. Session cookies are also SameSite=Lax.
func (s *Service) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CSRFCookie)
		if err != nil || c.Value == "" {
			c = &http.Cookie{Name: CSRFCookie, Value: randomToken(), Path: "/", Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
			http.SetCookie(w, c)
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			h := r.Header.Get(CSRFHeader)
			if h == "" || subtle.ConstantTimeCompare([]byte(h), []byte(c.Value)) != 1 {
				httpx.JSON(w, http.StatusForbidden, map[string]any{"error": httpx.Error{
					Code: "forbidden", Message: "Missing or invalid CSRF token. Reload the page and try again.",
					RequestID: httpx.GetRequestID(r.Context())}})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
