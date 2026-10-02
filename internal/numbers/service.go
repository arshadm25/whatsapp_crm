// Package numbers serves D5 phone number endpoints of the /v1 API (listing and health details).
// The business profile endpoints and health sync come with the D5 slice.
package numbers

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

type Service struct {
	db  *db.DB
	log *slog.Logger
}

func NewService(d *db.DB, log *slog.Logger) *Service { return &Service{db: d, log: log} }

func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
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
}

func view(p dbq.PhoneNumber, wabaID string) PhoneNumber {
	v := PhoneNumber{
		ID: p.ID, MetaPhoneNumberID: p.PhoneNumberID, WhatsappAccountID: p.WhatsappAccountID, WabaID: wabaID,
		DisplayPhoneNumber: p.DisplayPhoneNumber, VerifiedName: p.VerifiedName, NameStatus: p.NameStatus,
		QualityRating: string(p.QualityRating), IsCoexistence: p.IsCoexistence, Status: string(p.Status),
		LastSyncedAt: p.LastSyncedAt,
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
		for _, row := range rows {
			out = append(out, view(row.PhoneNumber, row.WabaID))
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
		out = view(row.PhoneNumber, row.WabaID)
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
