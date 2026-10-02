// Package devportal is D8: API keys for the public /v1 API, and (with the webhooks slice)
// client webhook endpoints and their delivery log.
package devportal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

const (
	livePrefix = "eco_live_"
	// prefixLen is how much of a key the dashboard shows to tell keys apart.
	prefixLen = len(livePrefix) + 4
)

// newKey returns a new live API key, its display prefix and its SHA-256 hash.
func newKey() (key, prefix string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	key = livePrefix + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(key))
	return key, key[:prefixLen], sum[:]
}

func hashKey(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

var (
	errBadAuth = httpx.NewError(http.StatusUnauthorized, "unauthorized",
		"Send your API key as Authorization: Bearer eco_live_....")
	errBadKey = httpx.NewError(http.StatusUnauthorized, "unauthorized", "This API key is invalid or has been revoked.")
)

// Authenticator lets /v1 requests in with either an API key or a dashboard session.
type Authenticator struct {
	db      *db.DB
	log     *slog.Logger
	limiter *Limiter
}

func NewAuthenticator(d *db.DB, limiter *Limiter, log *slog.Logger) *Authenticator {
	return &Authenticator{db: d, log: log, limiter: limiter}
}

// Middleware authenticates a request carrying an Authorization header as an API key, and
// sends every other request through session, the dashboard's cookie and CSRF chain.
func (a *Authenticator) Middleware(session func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		viaSession := session(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if h == "" {
				viaSession.ServeHTTP(w, r)
				return
			}
			key, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || !strings.HasPrefix(key, livePrefix) {
				httpx.WriteError(w, r, a.log, errBadAuth)
				return
			}
			p, err := a.resolve(r, key)
			if err != nil {
				httpx.WriteError(w, r, a.log, err)
				return
			}
			allowed, remaining, wait := a.limiter.Take(p.APIKeyID)
			secs := strconv.Itoa(int(math.Ceil(wait.Seconds())))
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(a.limiter.Limit()))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", secs)
			if !allowed {
				w.Header().Set("Retry-After", secs)
				httpx.WriteError(w, r, a.log, httpx.NewError(http.StatusTooManyRequests, "rate_limited",
					"Too many requests for this API key. Retry after "+secs+" second(s)."))
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		})
	}
}

func (a *Authenticator) resolve(r *http.Request, key string) (auth.Principal, error) {
	ctx := r.Context()
	var (
		p    auth.Principal
		mode dbq.ApiKeyMode
	)
	err := a.db.Global(ctx, func(_ *dbq.Queries, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT api_key_id, tenant_id, mode, phone_number_id FROM resolve_api_key($1)`, hashKey(key)).
			Scan(&p.APIKeyID, &p.TenantID, &mode, &p.KeyPhoneNumberID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return p, errBadKey
	}
	if err != nil {
		return p, err
	}
	if mode != dbq.ApiKeyModeLive {
		return p, errBadKey
	}
	// API keys act with admin rights in their workspace.
	p.Role = dbq.MemberRoleAdmin
	err = a.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error { return q.TouchAPIKey(ctx, p.APIKeyID) })
	return p, err
}

// Limiter is a token bucket per API key, kept in each api pod's memory. With n api pods a key
// can reach n times the limit; a shared Redis limiter can replace it when that matters.
type Limiter struct {
	rate float64 // tokens per second, also the burst size
	now  func() time.Time

	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
	sweep   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewLimiter(perSecond int) *Limiter {
	return &Limiter{rate: float64(perSecond), now: time.Now, buckets: map[uuid.UUID]*bucket{}}
}

func (l *Limiter) Limit() int { return int(l.rate) }

// Take spends one token for key. wait is how long until the bucket is full again, or, when no
// token is left, until the next one.
func (l *Limiter) Take(key uuid.UUID) (allowed bool, remaining int, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.sweep) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.last) > time.Minute {
				delete(l.buckets, k)
			}
		}
		l.sweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.rate, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.rate, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false, 0, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	}
	b.tokens--
	return true, int(b.tokens), time.Duration((l.rate - b.tokens) / l.rate * float64(time.Second))
}
