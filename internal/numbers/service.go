// Package numbers serves the D2/D5 phone number endpoints: listing, business profile and disconnecting.
package numbers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

// Meta is the part of the Graph API client the numbers endpoints use.
type Meta interface {
	GetBusinessProfile(ctx context.Context, token, phoneNumberID string) (*metaclient.BusinessProfile, error)
	UpdateBusinessProfile(ctx context.Context, token, phoneNumberID string, fields map[string]any) error
	UploadProfilePicture(ctx context.Context, token, mimeType, filename string, data []byte) (string, error)
	DeregisterPhoneNumber(ctx context.Context, token, phoneNumberID string) error
	ListPhoneNumbers(ctx context.Context, token, wabaID string) ([]metaclient.PhoneNumber, error)
}

type Service struct {
	db   *db.DB
	keys *envelope.Keyring
	meta Meta
	log  *slog.Logger
}

func NewService(d *db.DB, keys *envelope.Keyring, meta Meta, log *slog.Logger) *Service {
	return &Service{db: d, keys: keys, meta: meta, log: log}
}

// Routes mounts /v1/phone-numbers. Every member reads; owners and admins change profiles.
func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Get("/{id}/profile", httpx.Handler(s.log, s.getProfile))
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
		r.Patch("/{id}/profile", httpx.Handler(s.log, s.patchProfile))
		r.Post("/{id}/profile/logo", httpx.Handler(s.log, s.uploadLogo))
	})
}

// InternalRoutes mounts /internal/numbers for the dashboard.
func (s *Service) InternalRoutes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
	r.Post("/{id}/disconnect", httpx.Handler(s.log, s.disconnect))
	r.Post("/sync", httpx.Handler(s.log, s.sync))
}

// PhoneNumber matches the PhoneNumber schema in api/openapi.yaml.
type PhoneNumber struct {
	ID                 uuid.UUID  `json:"id"`
	MetaPhoneNumberID  string     `json:"meta_phone_number_id"`
	WhatsappAccountID  uuid.UUID  `json:"whatsapp_account_id"`
	WabaID             string     `json:"waba_id"`
	DisplayPhoneNumber string     `json:"display_phone_number"`
	VerifiedName       *string    `json:"verified_name"`
	NameStatus         *string    `json:"name_status"`
	QualityRating      string     `json:"quality_rating"`
	MessagingLimitTier *string    `json:"messaging_limit_tier"`
	IsCoexistence      bool       `json:"is_coexistence"`
	Status             string     `json:"status"`
	LastSyncedAt       *time.Time `json:"last_synced_at"`
	// RegisteredAt is when the number was registered with the Cloud API; null until then.
	RegisteredAt          *time.Time `json:"registered_at"`
	PreviousQualityRating *string    `json:"previous_quality_rating"`
	QualityChangedAt      *time.Time `json:"quality_changed_at"`
	// QualityDropped is true when the rating fell in the last 7 days.
	QualityDropped bool `json:"quality_dropped"`
	// DailyLimit is the messaging limit (-1 for unlimited) and LimitUsedToday the customers sent
	// a template in the last 24 hours. Returned by the list and GET only.
	DailyLimit     *int32 `json:"daily_limit,omitempty"`
	LimitUsedToday *int32 `json:"limit_used_today,omitempty"`
}

func View(p dbq.PhoneNumber, wabaID string) PhoneNumber {
	v := PhoneNumber{
		ID: p.ID, MetaPhoneNumberID: p.PhoneNumberID, WhatsappAccountID: p.WhatsappAccountID, WabaID: wabaID,
		DisplayPhoneNumber: p.DisplayPhoneNumber, VerifiedName: p.VerifiedName, NameStatus: p.NameStatus,
		QualityRating: string(p.QualityRating), IsCoexistence: p.IsCoexistence, Status: string(p.Status),
		LastSyncedAt: p.LastSyncedAt, RegisteredAt: p.RegisteredAt, QualityChangedAt: p.QualityChangedAt,
		QualityDropped: qualityDropped(p, time.Now()),
	}
	if p.PreviousQualityRating != nil {
		prev := string(*p.PreviousQualityRating)
		v.PreviousQualityRating = &prev
	}
	if p.MessagingLimitTier != nil {
		t := strings.ToUpper(*p.MessagingLimitTier)
		v.MessagingLimitTier = &t
	}
	return v
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []PhoneNumber{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListPhoneNumbers(r.Context())
		if err != nil {
			return err
		}
		used, err := usage(r.Context(), q)
		for _, row := range rows {
			out = append(out, withUsage(View(row.PhoneNumber, row.WabaID), row.PhoneNumber, used))
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var out PhoneNumber
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		row, err := q.GetPhoneNumber(r.Context(), id)
		if err != nil {
			return err
		}
		used, err := usage(r.Context(), q)
		out = withUsage(View(row.PhoneNumber, row.WabaID), row.PhoneNumber, used)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// usage returns how many customers each number sent a template in the last 24 hours.
func usage(ctx context.Context, q *dbq.Queries) (map[uuid.UUID]int32, error) {
	rows, err := q.NumbersUsageToday(ctx)
	out := map[uuid.UUID]int32{}
	for _, r := range rows {
		out[r.PhoneNumberID] = r.Used
	}
	return out, err
}

func withUsage(v PhoneNumber, p dbq.PhoneNumber, used map[uuid.UUID]int32) PhoneNumber {
	limit, n := DailyLimit(p.MessagingLimitTier), used[p.ID]
	v.DailyLimit, v.LimitUsedToday = &limit, &n
	return v
}
