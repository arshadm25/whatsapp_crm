// Package notifications serves the dashboard's notification bell and each member's notification
// settings, and emails the notifications members asked to get by email. Database triggers
// (migration 000021) create the notifications, so they appear whichever service made the change.
package notifications

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// Kind is one type of notification with its defaults. Keep in step with notification_default in
// migration 000021.
type Kind struct {
	Kind  string `json:"kind"`
	InApp bool   `json:"in_app"`
	Email bool   `json:"email"`
}

// Kinds lists every notification type with its default settings.
var Kinds = []Kind{
	{Kind: "conversation_assigned", InApp: true, Email: false},
	{Kind: "template_reviewed", InApp: true, Email: true},
	{Kind: "number_quality", InApp: true, Email: true},
	{Kind: "campaign_finished", InApp: true, Email: false},
}

type Service struct {
	db  *db.DB
	log *slog.Logger
}

func NewService(d *db.DB, log *slog.Logger) *Service {
	return &Service{db: d, log: log}
}

// Routes mounts /internal/notifications.
func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Post("/read", httpx.Handler(s.log, s.markRead))
	r.Get("/settings", httpx.Handler(s.log, s.settings))
	r.Put("/settings", httpx.Handler(s.log, s.updateSettings))
}

type Notification struct {
	ID        uuid.UUID  `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      *string    `json:"link"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := struct {
		Data   []Notification `json:"data"`
		Unread int32          `json:"unread"`
	}{Data: []Notification{}}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListNotifications(r.Context(), dbq.ListNotificationsParams{UserID: p.UserID, Lim: 30})
		if err != nil {
			return err
		}
		for _, n := range rows {
			out.Data = append(out.Data, Notification{ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body, Link: n.Link, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt})
		}
		out.Unread, err = q.CountUnreadNotifications(r.Context(), p.UserID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// markRead marks the listed notifications read, or all of them when ids is missing.
func (s *Service) markRead(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &req); err != nil {
			return err
		}
	}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.MarkNotificationsRead(r.Context(), dbq.MarkNotificationsReadParams{UserID: p.UserID, Ids: req.IDs})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) settings(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var saved []dbq.NotificationSetting
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		saved, err = q.ListNotificationSettings(r.Context(), p.UserID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": merge(saved)})
	return nil
}

// merge returns every kind with the member's choice, or the default when they have not chosen.
func merge(saved []dbq.NotificationSetting) []Kind {
	out := make([]Kind, len(Kinds))
	copy(out, Kinds)
	for i, k := range out {
		for _, s := range saved {
			if s.Kind == k.Kind {
				out[i].InApp, out[i].Email = s.InApp, s.Email
			}
		}
	}
	return out
}

func (s *Service) updateSettings(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Data []Kind `json:"data"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	for _, k := range req.Data {
		if !known(k.Kind) {
			return httpx.BadRequest("data", "Unknown notification type "+k.Kind+".")
		}
	}
	var saved []dbq.NotificationSetting
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		for _, k := range req.Data {
			if err := q.UpsertNotificationSetting(r.Context(), dbq.UpsertNotificationSettingParams{
				TenantID: p.TenantID, UserID: p.UserID, Kind: k.Kind, InApp: k.InApp, Email: k.Email,
			}); err != nil {
				return err
			}
		}
		var err error
		saved, err = q.ListNotificationSettings(r.Context(), p.UserID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": merge(saved)})
	return nil
}

func known(kind string) bool {
	for _, k := range Kinds {
		if k.Kind == kind {
			return true
		}
	}
	return false
}
