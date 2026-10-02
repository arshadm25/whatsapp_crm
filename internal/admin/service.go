// Package admin is A1, the platform admin console for Ecogo staff: every workspace with its
// numbers and volume, suspending and reactivating a workspace, Meta webhook health, Meta API
// errors, the audit log, and reading a conversation when a support case needs it. Every
// action is written to the audit log as a platform_admin; reading message content needs a
// stated reason.
package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

type Service struct {
	db  *db.DB
	log *slog.Logger
	now func() time.Time
}

func NewService(d *db.DB, log *slog.Logger) *Service {
	return &Service{db: d, log: log, now: time.Now}
}

// Routes mounts /internal/admin. The caller applies RequireSession, not RequireTenant: an
// admin works across workspaces.
func (s *Service) Routes(r chi.Router) {
	r.Use(RequireAdmin)
	r.Get("/tenants", httpx.Handler(s.log, s.listTenants))
	r.Get("/tenants/{id}", httpx.Handler(s.log, s.getTenant))
	r.Post("/tenants/{id}/suspend", httpx.Handler(s.log, s.suspend))
	r.Post("/tenants/{id}/reactivate", httpx.Handler(s.log, s.reactivate))
	r.Get("/tenants/{id}/conversations", httpx.Handler(s.log, s.listConversations))
	r.Post("/tenants/{id}/conversations/{cid}/messages", httpx.Handler(s.log, s.readMessages))
	r.Get("/webhook-health", httpx.Handler(s.log, s.webhookHealth))
	r.Get("/meta-errors", httpx.Handler(s.log, s.metaErrors))
	r.Get("/audit-log", httpx.Handler(s.log, s.auditLog))
	r.Get("/plans", httpx.Handler(s.log, s.listPlans))
	r.Put("/plans/{code}", httpx.Handler(s.log, s.savePlan))
	r.Get("/invoices", httpx.Handler(s.log, s.listInvoices))
	r.Get("/invoices.csv", httpx.Handler(s.log, s.exportInvoices))
	r.Get("/invoices/{id}/view", httpx.Handler(s.log, s.viewInvoice))
	r.Get("/meta-rates", httpx.Handler(s.log, s.listMetaRates))
	r.Put("/meta-rates", httpx.Handler(s.log, s.saveMetaRate))
	r.Delete("/meta-rates/{id}", httpx.Handler(s.log, s.deleteMetaRate))
	r.Post("/tenants/{id}/meta-payment-mode", httpx.Handler(s.log, s.setMetaPaymentMode))
	r.Get("/meta-fee-statements", httpx.Handler(s.log, s.listMetaFeeStatements))
	r.Get("/meta-fee-statements/{id}/view", httpx.Handler(s.log, s.viewMetaFeeStatement))
	r.Post("/meta-fee-statements/{id}/status", httpx.Handler(s.log, s.setMetaFeeStatementStatus))
	r.Post("/tenants/{id}/extend-trial", httpx.Handler(s.log, s.extendTrial))
	r.Get("/deletion-requests", httpx.Handler(s.log, s.listDeletionRequests))
	r.Post("/deletion-requests/{id}/status", httpx.Handler(s.log, s.setDeletionStatus))
}

// RequireAdmin hides the console from everyone but platform admins, who must have two-step
// verification on.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.PrincipalFrom(r.Context())
		if !p.PlatformAdmin || p.IsAPIKey() {
			httpx.WriteError(w, r, nil, httpx.NewError(http.StatusNotFound, "not_found", "Not found."))
			return
		}
		if !p.TOTPEnabled {
			httpx.WriteError(w, r, nil, httpx.NewError(http.StatusForbidden, "mfa_setup_required",
				"Turn on two-step verification in Settings before using the admin console."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) record(r *http.Request, q *dbq.Queries, tenantID *uuid.UUID, action, targetType, targetID string, reason *string) error {
	p, _ := auth.PrincipalFrom(r.Context())
	return q.InsertAdminAudit(r.Context(), dbq.InsertAdminAuditParams{
		TenantID: tenantID, ActorID: &p.UserID, Action: action, TargetType: &targetType, TargetID: &targetID,
		Reason: reason, Ip: auth.ClientIP(r),
	})
}

// Tenant is a workspace as the console lists it.
type Tenant struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	LegalName       *string   `json:"legal_name"`
	Status          string    `json:"status"`
	SuspendedReason *string   `json:"suspended_reason"`
	TimeZone        string    `json:"time_zone"`
	CreatedAt       time.Time `json:"created_at"`
	// MetaPaymentMode is who pays Meta for the workspace's messages: direct or through_us.
	MetaPaymentMode string `json:"meta_payment_mode"`
}

func tenantView(t dbq.Tenant) Tenant {
	return Tenant{ID: t.ID, Name: t.Name, Slug: t.Slug, LegalName: t.LegalName, Status: string(t.Status),
		SuspendedReason: t.SuspendedReason, TimeZone: t.Timezone, CreatedAt: t.CreatedAt, MetaPaymentMode: t.MetaPaymentMode}
}

func (s *Service) listTenants(w http.ResponseWriter, r *http.Request) error {
	lim, err := httpx.Limit(r.URL.Query().Get("limit"))
	if err != nil {
		return err
	}
	arg := dbq.AdminListTenantsParams{Lim: lim + 1}
	if v := r.URL.Query().Get("status"); v != "" {
		st := dbq.TenantStatus(v)
		if !st.Valid() {
			return httpx.BadRequest("status", "status must be active, suspended or closed.")
		}
		arg.Status = &st
	}
	if v := strings.TrimSpace(r.URL.Query().Get("q")); v != "" {
		arg.Search = &v
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		at, id, ok := httpx.DecodeCursor(c)
		if !ok {
			return httpx.BadRequest("cursor", "cursor is not valid.")
		}
		arg.BeforeAt, arg.BeforeID = &at, &id
	}
	var rows []dbq.Tenant
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err = q.AdminListTenants(r.Context(), arg)
		return err
	})
	if err != nil {
		return err
	}
	var next *string
	if len(rows) > int(lim) {
		rows = rows[:lim]
		c := httpx.EncodeCursor(rows[lim-1].CreatedAt, rows[lim-1].ID)
		next = &c
	}
	out := make([]Tenant, len(rows))
	for i, t := range rows {
		out[i] = tenantView(t)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

// TenantDetail is one workspace's health at a glance. It has counts, never message content.
type TenantDetail struct {
	Tenant
	Members          int32                 `json:"members"`
	Numbers          []Number              `json:"numbers"`
	Sent30d          int32                 `json:"sent_30d"`
	Received30d      int32                 `json:"received_30d"`
	LastMessageAt    *time.Time            `json:"last_message_at"`
	WebhookDelivered map[string]int32      `json:"webhook_deliveries_24h"`
	Subscription     *billing.Subscription `json:"subscription"`
}

type Number struct {
	ID                 uuid.UUID `json:"id"`
	DisplayPhoneNumber string    `json:"display_phone_number"`
	VerifiedName       *string   `json:"verified_name"`
	Status             string    `json:"status"`
	QualityRating      string    `json:"quality_rating"`
	MessagingLimitTier *string   `json:"messaging_limit_tier"`
	Coexistence        bool      `json:"coexistence"`
	WabaID             string    `json:"waba_id"`
}

func tenantID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return id, httpx.NewError(http.StatusNotFound, "not_found", "Workspace not found.")
	}
	return id, nil
}

func (s *Service) getTenant(w http.ResponseWriter, r *http.Request) error {
	id, err := tenantID(r)
	if err != nil {
		return err
	}
	var d TenantDetail
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		t, err := q.GetTenant(r.Context(), id)
		if db.IsNotFound(err) {
			return httpx.NewError(http.StatusNotFound, "not_found", "Workspace not found.")
		}
		d.Tenant = tenantView(t)
		return err
	})
	if err != nil {
		return err
	}
	err = s.db.InTenant(r.Context(), id, func(q *dbq.Queries, _ pgx.Tx) error {
		c, err := q.AdminTenantCounts(r.Context())
		if err != nil {
			return err
		}
		d.Members, d.Sent30d, d.Received30d = c.Members, c.Sent30d, c.Received30d
		last, err := q.AdminLastMessageAt(r.Context())
		if err == nil {
			d.LastMessageAt = &last
		} else if !db.IsNotFound(err) {
			return err
		}
		nums, err := q.AdminTenantNumbers(r.Context())
		if err != nil {
			return err
		}
		d.Numbers = make([]Number, len(nums))
		for i, n := range nums {
			d.Numbers[i] = Number{ID: n.ID, DisplayPhoneNumber: n.DisplayPhoneNumber, VerifiedName: n.VerifiedName,
				Status: string(n.Status), QualityRating: string(n.QualityRating), MessagingLimitTier: n.MessagingLimitTier,
				Coexistence: n.IsCoexistence, WabaID: n.WabaID}
		}
		stats, err := q.AdminDeliveryStats(r.Context(), s.now().Add(-24*time.Hour))
		if err != nil {
			return err
		}
		d.WebhookDelivered = map[string]int32{}
		for _, st := range stats {
			d.WebhookDelivered[string(st.Status)] = st.N
		}
		sub, err := q.GetSubscription(r.Context())
		if err == nil {
			v := billing.SubscriptionView(sub, s.now())
			d.Subscription = &v
		} else if !db.IsNotFound(err) {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, d)
	return nil
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

// reason reads the required free-text justification for an admin action.
func reason(r *http.Request) (string, error) {
	var req reasonRequest
	if err := httpx.Decode(r, &req); err != nil {
		return "", err
	}
	v := strings.TrimSpace(req.Reason)
	if len(v) < 5 || len(v) > 500 {
		return "", httpx.BadRequest("reason", "Give a reason of 5 to 500 characters; it is kept in the audit log.")
	}
	return v, nil
}

func (s *Service) suspend(w http.ResponseWriter, r *http.Request) error {
	return s.setStatus(w, r, dbq.TenantStatusSuspended, "tenant.suspend")
}

func (s *Service) reactivate(w http.ResponseWriter, r *http.Request) error {
	return s.setStatus(w, r, dbq.TenantStatusActive, "tenant.reactivate")
}

// setStatus suspends or reactivates a workspace. A suspended workspace's sessions and API
// keys stop working on their next request (auth checks the tenant's status every time).
func (s *Service) setStatus(w http.ResponseWriter, r *http.Request, to dbq.TenantStatus, action string) error {
	id, err := tenantID(r)
	if err != nil {
		return err
	}
	why, err := reason(r)
	if err != nil {
		return err
	}
	var t dbq.Tenant
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		cur, err := q.GetTenant(r.Context(), id)
		if db.IsNotFound(err) {
			return httpx.NewError(http.StatusNotFound, "not_found", "Workspace not found.")
		}
		if err != nil {
			return err
		}
		if cur.Status == dbq.TenantStatusClosed {
			return httpx.NewError(http.StatusConflict, "conflict", "This workspace is closed.")
		}
		var stored *string
		if to == dbq.TenantStatusSuspended {
			stored = &why
		}
		t, err = q.AdminSetTenantStatus(r.Context(), dbq.AdminSetTenantStatusParams{Status: to, Reason: stored, ID: id})
		if err != nil {
			return err
		}
		return s.record(r, q, &id, action, "tenant", id.String(), &why)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, tenantView(t))
	return nil
}

// Conversation is the metadata a support case needs to pick a thread; no message content.
type Conversation struct {
	ID            uuid.UUID  `json:"id"`
	PhoneNumberID uuid.UUID  `json:"phone_number_id"`
	ContactWaID   string     `json:"contact_wa_id"`
	Status        string     `json:"status"`
	LastMessageAt *time.Time `json:"last_message_at"`
}

func (s *Service) listConversations(w http.ResponseWriter, r *http.Request) error {
	id, err := tenantID(r)
	if err != nil {
		return err
	}
	lim, err := httpx.Limit(r.URL.Query().Get("limit"))
	if err != nil {
		return err
	}
	arg := dbq.ListConversationsParams{Lim: lim + 1}
	if v := strings.TrimSpace(r.URL.Query().Get("q")); v != "" {
		arg.Search = &v
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		at, cid, ok := httpx.DecodeCursor(c)
		if !ok {
			return httpx.BadRequest("cursor", "cursor is not valid.")
		}
		arg.BeforeAt, arg.BeforeID = &at, &cid
	}
	var rows []dbq.ListConversationsRow
	err = s.db.InTenant(r.Context(), id, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err = q.ListConversations(r.Context(), arg)
		return err
	})
	if err != nil {
		return err
	}
	var next *string
	if len(rows) > int(lim) {
		rows = rows[:lim]
		c := httpx.EncodeCursor(rows[lim-1].ActivityAt, rows[lim-1].Conversation.ID)
		next = &c
	}
	out := make([]Conversation, len(rows))
	for i, row := range rows {
		out[i] = Conversation{ID: row.Conversation.ID, PhoneNumberID: row.Conversation.PhoneNumberID,
			ContactWaID: row.Contact.WaID, Status: string(row.Conversation.Status), LastMessageAt: row.Conversation.LastMessageAt}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

// readMessages shows a conversation's latest messages, content included. The reason is
// required and the read is audited as message_content.view in the workspace's log.
func (s *Service) readMessages(w http.ResponseWriter, r *http.Request) error {
	id, err := tenantID(r)
	if err != nil {
		return err
	}
	cid, err := uuid.Parse(chi.URLParam(r, "cid"))
	if err != nil {
		return httpx.NewError(http.StatusNotFound, "not_found", "Conversation not found.")
	}
	why, err := reason(r)
	if err != nil {
		return err
	}
	var out []messaging.Message
	err = s.db.InTenant(r.Context(), id, func(q *dbq.Queries, _ pgx.Tx) error {
		cv, err := q.GetConversationView(r.Context(), cid)
		if db.IsNotFound(err) {
			return httpx.NewError(http.StatusNotFound, "not_found", "Conversation not found.")
		}
		if err != nil {
			return err
		}
		msgs, err := q.ListConversationMessages(r.Context(), dbq.ListConversationMessagesParams{ConversationID: cid, Lim: 50})
		if err != nil {
			return err
		}
		out = make([]messaging.Message, len(msgs))
		for i, m := range msgs {
			out[i] = messaging.View(m, cv.Contact.WaID, cv.Contact.Name)
		}
		return s.record(r, q, &id, "message_content.view", "conversation", cid.String(), &why)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// WebhookHealth is how Meta's webhook deliveries have fared over the last hours.
type WebhookHealth struct {
	Hours  []WebhookHour  `json:"hours"`
	Errors []WebhookError `json:"errors"`
}

type WebhookHour struct {
	Hour          time.Time `json:"hour"`
	Received      int32     `json:"received"`
	Processed     int32     `json:"processed"`
	Failed        int32     `json:"failed"`
	Pending       int32     `json:"pending"`
	P95LagSeconds float64   `json:"p95_lag_seconds"`
}

type WebhookError struct {
	ID         int64      `json:"id"`
	ReceivedAt time.Time  `json:"received_at"`
	Field      *string    `json:"field"`
	WabaID     *string    `json:"waba_id"`
	TenantID   *uuid.UUID `json:"tenant_id"`
	Error      *string    `json:"error"`
}

func (s *Service) webhookHealth(w http.ResponseWriter, r *http.Request) error {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 168 {
			return httpx.BadRequest("hours", "hours must be between 1 and 168.")
		}
		hours = n
	}
	since := s.now().Add(-time.Duration(hours) * time.Hour)
	out := WebhookHealth{Hours: []WebhookHour{}, Errors: []WebhookError{}}
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		hs, err := q.AdminWebhookHourly(r.Context(), since)
		if err != nil {
			return err
		}
		for _, h := range hs {
			out.Hours = append(out.Hours, WebhookHour(h))
		}
		es, err := q.AdminWebhookErrors(r.Context(), since)
		if err != nil {
			return err
		}
		for _, e := range es {
			out.Errors = append(out.Errors, WebhookError(e))
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// idCursor pages lists ordered by a bigserial ID.
func idCursor(r *http.Request) (*int64, int32, error) {
	lim, err := httpx.Limit(r.URL.Query().Get("limit"))
	if err != nil {
		return nil, 0, err
	}
	c := r.URL.Query().Get("cursor")
	if c == "" {
		return nil, lim, nil
	}
	n, err := strconv.ParseInt(c, 10, 64)
	if err != nil {
		return nil, 0, httpx.BadRequest("cursor", "cursor is not valid.")
	}
	return &n, lim, nil
}

func optionalUUID(r *http.Request, name string) (*uuid.UUID, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, httpx.BadRequest(name, name+" must be a UUID.")
	}
	return &id, nil
}

type MetaError struct {
	ID         int64      `json:"id"`
	TenantID   *uuid.UUID `json:"tenant_id"`
	Method     string     `json:"method"`
	Path       string     `json:"path"`
	HTTPStatus int32      `json:"http_status"`
	Code       *int32     `json:"code"`
	Subcode    *int32     `json:"subcode"`
	Message    *string    `json:"message"`
	FbtraceID  *string    `json:"fbtrace_id"`
	OccurredAt time.Time  `json:"occurred_at"`
}

func (s *Service) metaErrors(w http.ResponseWriter, r *http.Request) error {
	before, lim, err := idCursor(r)
	if err != nil {
		return err
	}
	tid, err := optionalUUID(r, "tenant_id")
	if err != nil {
		return err
	}
	var rows []dbq.MetaApiError
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err = q.AdminMetaErrors(r.Context(), dbq.AdminMetaErrorsParams{TenantID: tid, BeforeID: before, Lim: lim + 1})
		return err
	})
	if err != nil {
		return err
	}
	var next *string
	if len(rows) > int(lim) {
		rows = rows[:lim]
		c := strconv.FormatInt(rows[lim-1].ID, 10)
		next = &c
	}
	out := make([]MetaError, len(rows))
	for i, e := range rows {
		out[i] = MetaError{ID: e.ID, TenantID: e.TenantID, Method: e.Method, Path: e.Path, HTTPStatus: e.HttpStatus,
			Code: e.Code, Subcode: e.Subcode, Message: e.Message, FbtraceID: e.FbtraceID, OccurredAt: e.OccurredAt}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

type AuditEntry struct {
	ID         int64           `json:"id"`
	TenantID   *uuid.UUID      `json:"tenant_id"`
	ActorType  string          `json:"actor_type"`
	ActorID    *uuid.UUID      `json:"actor_id"`
	ActorEmail *string         `json:"actor_email"`
	Action     string          `json:"action"`
	TargetType *string         `json:"target_type"`
	TargetID   *string         `json:"target_id"`
	Reason     *string         `json:"reason"`
	IP         *string         `json:"ip"`
	Metadata   json.RawMessage `json:"metadata"`
	OccurredAt time.Time       `json:"occurred_at"`
}

func (s *Service) auditLog(w http.ResponseWriter, r *http.Request) error {
	before, lim, err := idCursor(r)
	if err != nil {
		return err
	}
	arg := dbq.AdminAuditLogParams{BeforeID: before, Lim: lim + 1}
	if arg.TenantID, err = optionalUUID(r, "tenant_id"); err != nil {
		return err
	}
	if v := r.URL.Query().Get("actor_type"); v != "" {
		at := dbq.ActorType(v)
		if !at.Valid() {
			return httpx.BadRequest("actor_type", "actor_type is not valid.")
		}
		arg.ActorType = &at
	}
	var rows []dbq.AdminAuditLogRow
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err = q.AdminAuditLog(r.Context(), arg)
		return err
	})
	if err != nil {
		return err
	}
	var next *string
	if len(rows) > int(lim) {
		rows = rows[:lim]
		c := strconv.FormatInt(rows[lim-1].ID, 10)
		next = &c
	}
	out := make([]AuditEntry, len(rows))
	for i, a := range rows {
		e := AuditEntry{ID: a.ID, TenantID: a.TenantID, ActorType: string(a.ActorType), ActorID: a.ActorID,
			ActorEmail: a.ActorEmail, Action: a.Action, TargetType: a.TargetType, TargetID: a.TargetID,
			Reason: a.Reason, Metadata: a.Metadata, OccurredAt: a.OccurredAt}
		if a.Ip != nil {
			ip := a.Ip.String()
			e.IP = &ip
		}
		out[i] = e
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

func (s *Service) listPlans(w http.ResponseWriter, r *http.Request) error {
	out := []billing.Plan{}
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		plans, err := q.ListPlans(r.Context(), false)
		for _, p := range plans {
			out = append(out, billing.PlanView(p))
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

var planCode = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// savePlan creates or updates a plan. Prices are in paise. A plan needs its Razorpay plan
// (created in the Razorpay dashboard with the same monthly amount) before customers can pay.
func (s *Service) savePlan(w http.ResponseWriter, r *http.Request) error {
	code := chi.URLParam(r, "code")
	if !planCode.MatchString(code) {
		return httpx.BadRequest("code", "Use 2 to 32 lowercase letters, digits or underscores, starting with a letter.")
	}
	var req billing.Plan
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	switch {
	case req.Name == "" || len(req.Name) > 60:
		return httpx.BadRequest("name", "Give the plan a name of up to 60 characters.")
	case req.PriceMinor < 0 || req.ExtraSeatMinor < 0:
		return httpx.BadRequest("price_minor", "Prices cannot be negative.")
	case req.IncludedNumbers < 1 || req.IncludedSeats < 1:
		return httpx.BadRequest("included_numbers", "A plan includes at least one number and one seat.")
	case req.AIRepliesPerMonth < 0 || req.AIRepliesPerMonth > 10_000_000:
		return httpx.BadRequest("ai_replies_per_month", "AI replies per month must be between 0 and 10,000,000.")
	}
	if req.RazorpayPlanID != nil {
		v := strings.TrimSpace(*req.RazorpayPlanID)
		if v == "" {
			req.RazorpayPlanID = nil
		} else if !strings.HasPrefix(v, "plan_") {
			return httpx.BadRequest("razorpay_plan_id", "Razorpay plan IDs start with plan_.")
		} else {
			req.RazorpayPlanID = &v
		}
	}
	if req.ExtraSeatRazorpayPlanID != nil {
		v := strings.TrimSpace(*req.ExtraSeatRazorpayPlanID)
		if v == "" {
			req.ExtraSeatRazorpayPlanID = nil
		} else if !strings.HasPrefix(v, "plan_") {
			return httpx.BadRequest("extra_seat_razorpay_plan_id", "Razorpay plan IDs start with plan_.")
		} else {
			req.ExtraSeatRazorpayPlanID = &v
		}
	}
	var out dbq.Plan
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		out, err = q.UpsertPlan(r.Context(), dbq.UpsertPlanParams{
			Code: code, Name: req.Name, PriceMinor: req.PriceMinor, IncludedNumbers: req.IncludedNumbers,
			IncludedSeats: req.IncludedSeats, ExtraSeatMinor: req.ExtraSeatMinor, RazorpayPlanID: req.RazorpayPlanID,
			ExtraSeatRazorpayPlanID: req.ExtraSeatRazorpayPlanID, SortOrder: req.SortOrder, IsActive: req.Active,
			AiRepliesPerMonth: req.AIRepliesPerMonth,
		})
		if db.IsUniqueViolation(err, "plans_razorpay_plan_id_key") {
			return httpx.BadRequest("razorpay_plan_id", "Another plan already uses this Razorpay plan.")
		}
		if err != nil {
			return err
		}
		return s.record(r, q, nil, "plan.save", "plan", code, nil)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, billing.PlanView(out))
	return nil
}

// extendTrial gives a workspace on trial more days, e.g. while it finishes Meta verification.
func (s *Service) extendTrial(w http.ResponseWriter, r *http.Request) error {
	id, err := tenantID(r)
	if err != nil {
		return err
	}
	var req struct {
		Days   int    `json:"days"`
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if req.Days < 1 || req.Days > 90 {
		return httpx.BadRequest("days", "Extend by 1 to 90 days.")
	}
	why := strings.TrimSpace(req.Reason)
	if len(why) < 5 || len(why) > 500 {
		return httpx.BadRequest("reason", "Give a reason of 5 to 500 characters; it is kept in the audit log.")
	}
	var out billing.Subscription
	err = s.db.InTenant(r.Context(), id, func(q *dbq.Queries, _ pgx.Tx) error {
		sub, err := q.GetSubscription(r.Context())
		if err != nil {
			return err
		}
		if sub.Status != dbq.SubscriptionStatusTrialing {
			return httpx.NewError(http.StatusConflict, "conflict", "Only a workspace on trial can have its trial extended.")
		}
		from := sub.CurrentPeriodEnd
		if now := s.now(); from.Before(now) {
			from = now
		}
		sub, err = q.ExtendTrial(r.Context(), dbq.ExtendTrialParams{Until: from.AddDate(0, 0, req.Days), TenantID: id})
		if err != nil {
			return err
		}
		out = billing.SubscriptionView(sub, s.now())
		return s.record(r, q, &id, "subscription.extend_trial", "tenant", id.String(), &why)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// DeletionRequest is a Meta data deletion callback waiting for staff to act on it.
type DeletionRequest struct {
	ID               uuid.UUID  `json:"id"`
	ConfirmationCode string     `json:"confirmation_code"`
	MetaUserID       string     `json:"meta_user_id"`
	Status           string     `json:"status"`
	RequestedAt      time.Time  `json:"requested_at"`
	CompletedAt      *time.Time `json:"completed_at"`
}

func deletionView(d dbq.DataDeletionRequest) DeletionRequest {
	return DeletionRequest{ID: d.ID, ConfirmationCode: d.ConfirmationCode, MetaUserID: d.MetaUserID,
		Status: d.Status, RequestedAt: d.RequestedAt, CompletedAt: d.CompletedAt}
}

func (s *Service) listDeletionRequests(w http.ResponseWriter, r *http.Request) error {
	var rows []dbq.DataDeletionRequest
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		rows, err = q.ListDeletionRequests(r.Context())
		return err
	})
	if err != nil {
		return err
	}
	out := make([]DeletionRequest, len(rows))
	for i, d := range rows {
		out[i] = deletionView(d)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Service) setDeletionStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if req.Status != "in_progress" && req.Status != "completed" {
		return httpx.BadRequest("status", "Status must be in_progress or completed.")
	}
	var out DeletionRequest
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		d, err := q.SetDeletionRequestStatus(r.Context(), dbq.SetDeletionRequestStatusParams{ID: id, Status: req.Status})
		if err != nil {
			return err
		}
		out = deletionView(d)
		return s.record(r, q, nil, "deletion_request."+req.Status, "data_deletion_request", id.String(), nil)
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
