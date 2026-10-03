package devportal

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// Service serves the dashboard's Developers screen under /internal/developers.
type Service struct {
	db  *db.DB
	log *slog.Logger
}

func NewService(d *db.DB, log *slog.Logger) *Service { return &Service{db: d, log: log} }

func (s *Service) InternalRoutes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin, dbq.MemberRoleDeveloper))
	r.Get("/api-keys", httpx.Handler(s.log, s.listKeys))
	r.Post("/api-keys", httpx.Handler(s.log, s.createKey))
	r.Delete("/api-keys/{id}", httpx.Handler(s.log, s.revokeKey))
	r.Get("/stats", httpx.Handler(s.log, s.stats))
}

// APIKey is an API key as the dashboard lists it; the key itself is never stored.
type APIKey struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Prefix        string     `json:"prefix"`
	Mode          string     `json:"mode"`
	PhoneNumberID *uuid.UUID `json:"phone_number_id"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func keyView(k dbq.ApiKey) APIKey {
	return APIKey{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Mode: string(k.Mode), PhoneNumberID: k.PhoneNumberID,
		LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt, CreatedAt: k.CreatedAt}
}

func (s *Service) listKeys(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []APIKey{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		keys, err := q.ListAPIKeys(r.Context())
		for _, k := range keys {
			out = append(out, keyView(k))
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// CreatedKey carries the full key, returned only in the response that creates it.
type CreatedKey struct {
	APIKey
	Key string `json:"key"`
}

func (s *Service) createKey(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Name          string     `json:"name"`
		PhoneNumberID *uuid.UUID `json:"phone_number_id"`
		Mode          string     `json:"mode"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 80 {
		return httpx.BadRequest("name", "Give the key a name of up to 80 characters, such as the system that will use it.")
	}
	mode := dbq.ApiKeyModeLive
	switch req.Mode {
	case "", "live":
	case "sandbox":
		mode = dbq.ApiKeyModeSandbox
	default:
		return httpx.BadRequest("mode", "Mode must be live or sandbox.")
	}
	key, prefix, hash := newKey(mode)
	var k dbq.ApiKey
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if req.PhoneNumberID != nil {
			if _, err := q.GetSendingNumber(r.Context(), *req.PhoneNumberID); err != nil {
				if db.IsNotFound(err) {
					return httpx.BadRequest("phone_number_id", "Phone number not found.")
				}
				return err
			}
		}
		var err error
		k, err = q.InsertAPIKey(r.Context(), dbq.InsertAPIKeyParams{
			ID: db.NewID(), TenantID: p.TenantID, Name: req.Name, Prefix: prefix, KeyHash: hash,
			Mode: mode, PhoneNumberID: req.PhoneNumberID, CreatedBy: p.User(),
		})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, CreatedKey{APIKey: keyView(k), Key: key})
	return nil
}

func (s *Service) revokeKey(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var n int64
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		n, err = q.RevokeAPIKey(r.Context(), id)
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return httpx.ErrNotFound
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
