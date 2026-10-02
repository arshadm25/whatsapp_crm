// Package campaigns sends an approved template to a list of contacts (D7). Creating a campaign
// checks the number, the template and its variables, and schedules a run job. The run expands
// the audience into recipients (skipping blocked, opted-out and never-opted-in contacts), then
// queues messages in batches no faster than the campaign's rate and the number's messaging
// limit. Each message goes through the normal send worker; its delivery statuses flow back to
// the recipient row through a database trigger.
package campaigns

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

const (
	maxContactIDs = 10000
	maxTags       = 50
	maxRate       = 10000
	// DefaultRate is the send rate when a campaign does not set one, in messages per minute.
	DefaultRate = 1000
)

type Service struct {
	db       *db.DB
	inserter jobs.Inserter
	log      *slog.Logger
	now      func() time.Time
}

func NewService(d *db.DB, inserter jobs.Inserter, log *slog.Logger) *Service {
	return &Service{db: d, inserter: inserter, log: log, now: time.Now}
}

var manage = auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin)

// Routes mounts /v1/campaigns.
func (s *Service) Routes(r chi.Router) {
	r.Use(manage)
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Post("/", httpx.Handler(s.log, s.create))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Post("/{id}/cancel", httpx.Handler(s.log, s.cancel))
}

// InternalRoutes mounts the dashboard's /internal/campaigns: an audience preview and the
// per-recipient report.
func (s *Service) InternalRoutes(r chi.Router) {
	r.Use(manage)
	r.Post("/audience", httpx.Handler(s.log, s.audience))
	r.Get("/{id}/recipients", httpx.Handler(s.log, s.recipients))
}

type Audience struct {
	Tags       []string    `json:"tags,omitempty"`
	ContactIDs []uuid.UUID `json:"contact_ids,omitempty"`
}

type TemplateInput struct {
	Name      string            `json:"name"`
	Language  string            `json:"language"`
	Variables map[string]string `json:"variables"`
}

type CreateRequest struct {
	Name           string        `json:"name"`
	PhoneNumberID  uuid.UUID     `json:"phone_number_id"`
	Template       TemplateInput `json:"template"`
	Audience       Audience      `json:"audience"`
	ScheduledAt    *time.Time    `json:"scheduled_at"`
	SendRatePerMin *int32        `json:"send_rate_per_min"`
}

type TemplateRef struct {
	Name      string            `json:"name"`
	Language  string            `json:"language"`
	Variables map[string]string `json:"variables"`
}

// Stats counts recipients by status. sent, delivered and read are cumulative: a read message
// counts as delivered and sent too.
type Stats struct {
	Total     int32 `json:"total"`
	Pending   int32 `json:"pending"`
	Skipped   int32 `json:"skipped"`
	Queued    int32 `json:"queued"`
	Sent      int32 `json:"sent"`
	Delivered int32 `json:"delivered"`
	Read      int32 `json:"read"`
	Failed    int32 `json:"failed"`
}

type Campaign struct {
	ID             uuid.UUID   `json:"id"`
	Name           string      `json:"name"`
	Status         string      `json:"status"`
	PhoneNumberID  uuid.UUID   `json:"phone_number_id"`
	TemplateID     uuid.UUID   `json:"template_id"`
	Template       TemplateRef `json:"template"`
	Audience       Audience    `json:"audience"`
	ScheduledAt    *time.Time  `json:"scheduled_at"`
	StartedAt      *time.Time  `json:"started_at"`
	FinishedAt     *time.Time  `json:"finished_at"`
	SendRatePerMin int32       `json:"send_rate_per_min"`
	Stats          Stats       `json:"stats"`
	CreatedAt      time.Time   `json:"created_at"`
}

func view(c dbq.Campaign, t dbq.Template, st Stats) Campaign {
	var a Audience
	_ = json.Unmarshal(c.Audience, &a)
	vars := map[string]string{}
	_ = json.Unmarshal(c.Variables, &vars)
	rate := int32(DefaultRate)
	if c.SendRatePerMin != nil {
		rate = *c.SendRatePerMin
	}
	return Campaign{
		ID: c.ID, Name: c.Name, Status: string(c.Status), PhoneNumberID: c.PhoneNumberID, TemplateID: c.TemplateID,
		Template: TemplateRef{Name: t.Name, Language: t.Language, Variables: vars}, Audience: a,
		ScheduledAt: c.ScheduledAt, StartedAt: c.StartedAt, FinishedAt: c.FinishedAt, SendRatePerMin: rate,
		Stats: st, CreatedAt: c.CreatedAt,
	}
}

func unprocessable(code, param, message string) error {
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: code, Param: param, Message: message}
}

func notFound() error {
	return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Message: "Campaign not found."}
}

func (a *Audience) clean() error {
	if len(a.ContactIDs) > maxContactIDs {
		return httpx.BadRequest("audience.contact_ids", "Send at most 10000 contact_ids; use tags for larger audiences.")
	}
	var tags []string
	seen := map[string]bool{}
	for _, t := range a.Tags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			tags = append(tags, t)
		}
	}
	if len(tags) > maxTags {
		return httpx.BadRequest("audience.tags", "Send at most 50 tags.")
	}
	a.Tags = tags
	if len(a.Tags) == 0 && len(a.ContactIDs) == 0 {
		return httpx.BadRequest("audience", "audience needs tags or contact_ids.")
	}
	return nil
}

// params returns the audience as query arguments; empty lists, not NULL, so ANY() matches nothing.
func (a Audience) params() ([]uuid.UUID, []string) {
	ids, tags := a.ContactIDs, a.Tags
	if ids == nil {
		ids = []uuid.UUID{}
	}
	if tags == nil {
		tags = []string{}
	}
	return ids, tags
}

func (req *CreateRequest) check(now time.Time) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 100 {
		return httpx.BadRequest("name", "name must have 1 to 100 characters.")
	}
	if req.PhoneNumberID == uuid.Nil {
		return httpx.BadRequest("phone_number_id", "phone_number_id is required.")
	}
	if req.Template.Name == "" || req.Template.Language == "" {
		return httpx.BadRequest("template", "template needs a name and a language.")
	}
	if req.Template.Variables == nil {
		req.Template.Variables = map[string]string{}
	}
	if err := req.Audience.clean(); err != nil {
		return err
	}
	if req.ScheduledAt != nil {
		if req.ScheduledAt.Before(now.Add(-time.Minute)) {
			return httpx.BadRequest("scheduled_at", "scheduled_at is in the past; leave it out to send now.")
		}
		if req.ScheduledAt.After(now.AddDate(0, 0, 90)) {
			return httpx.BadRequest("scheduled_at", "scheduled_at can be at most 90 days ahead.")
		}
	}
	if r := req.SendRatePerMin; r != nil && (*r < 1 || *r > maxRate) {
		return httpx.BadRequest("send_rate_per_min", "send_rate_per_min must be between 1 and 10000.")
	}
	return nil
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return httpx.BadRequest("", "Could not read the request body.")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var req CreateRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 255 {
		return httpx.BadRequest("Idempotency-Key", "Idempotency-Key must be at most 255 characters.")
	}
	now := s.now()
	if err := req.check(now); err != nil {
		return err
	}

	var (
		out    Campaign
		cached *messaging.Replay
	)
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		if err := billing.Check(ctx, q, now); err != nil {
			return err
		}
		if key != "" {
			hash := sha256.Sum256(raw)
			if cached, err = messaging.ClaimIdempotencyKey(ctx, q, p.TenantID, key, hash[:], now); err != nil || cached != nil {
				return err
			}
		}
		if out, err = s.insert(ctx, q, tx, p, &req, now); err != nil {
			return err
		}
		if key != "" {
			body, _ := json.Marshal(out)
			status := int32(http.StatusCreated)
			return q.SaveIdempotencyResponse(ctx, dbq.SaveIdempotencyResponseParams{
				TenantID: p.TenantID, Key: key, ResponseStatus: &status, ResponseBody: body,
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if cached != nil {
		messaging.WriteReplay(w, cached)
		return nil
	}
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

func (s *Service) insert(ctx context.Context, q *dbq.Queries, tx pgx.Tx, p auth.Principal, req *CreateRequest, now time.Time) (Campaign, error) {
	num, err := q.GetSendingNumber(ctx, req.PhoneNumberID)
	if db.IsNotFound(err) || (err == nil && !p.AllowsNumber(num.PhoneNumber.ID)) {
		return Campaign{}, &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Param: "phone_number_id", Message: "Phone number not found."}
	}
	if err != nil {
		return Campaign{}, err
	}
	if num.PhoneNumber.Status != dbq.ConnectionStatusConnected || num.WhatsappAccount.Status != dbq.ConnectionStatusConnected {
		return Campaign{}, unprocessable("number_not_connected", "phone_number_id", "This number is not connected. Reconnect it from Numbers.")
	}
	t, err := q.GetTemplateByName(ctx, dbq.GetTemplateByNameParams{
		WhatsappAccountID: num.WhatsappAccount.ID, Name: req.Template.Name, Language: req.Template.Language,
	})
	if db.IsNotFound(err) || (err == nil && t.Status != dbq.TemplateStatusApproved) {
		return Campaign{}, unprocessable("template_not_approved", "template.name",
			"There is no approved template with this name and language on this number's account.")
	}
	if err != nil {
		return Campaign{}, err
	}
	if err := checkVariables(t, req.Template.Variables); err != nil {
		return Campaign{}, err
	}
	ids, tags := req.Audience.params()
	counts, err := q.AudienceCounts(ctx, dbq.AudienceCountsParams{ContactIds: ids, Tags: tags})
	if err != nil {
		return Campaign{}, err
	}
	if counts.Total == 0 {
		return Campaign{}, unprocessable("audience_empty", "audience", "No contacts match this audience.")
	}

	audience, _ := json.Marshal(req.Audience)
	vars, _ := json.Marshal(req.Template.Variables)
	var keyID *uuid.UUID
	if p.IsAPIKey() {
		keyID = &p.APIKeyID
	}
	c, err := q.InsertCampaign(ctx, dbq.InsertCampaignParams{
		ID: db.NewID(), TenantID: p.TenantID, PhoneNumberID: num.PhoneNumber.ID, TemplateID: t.ID, Name: req.Name,
		Audience: audience, Variables: vars, ScheduledAt: req.ScheduledAt, SendRatePerMin: req.SendRatePerMin,
		CreatedBy: p.User(), ApiKeyID: keyID,
	})
	if err != nil {
		return Campaign{}, err
	}
	opts := &river.InsertOpts{}
	if req.ScheduledAt != nil && req.ScheduledAt.After(now) {
		opts.ScheduledAt = *req.ScheduledAt
	}
	if _, err := s.inserter.InsertTx(ctx, tx, RunArgs{CampaignID: c.ID, TenantID: p.TenantID}, opts); err != nil {
		return Campaign{}, err
	}
	return view(c, t, Stats{}), nil
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	lim, err := httpx.Limit(qs.Get("limit"))
	if err != nil {
		return err
	}
	// A key limited to one number sees only that number's campaigns.
	params := dbq.ListCampaignsParams{Lim: lim + 1, PhoneNumberID: p.KeyPhoneNumberID}
	if v := qs.Get("cursor"); v != "" {
		at, id, ok := httpx.DecodeCursor(v)
		if !ok {
			return httpx.BadRequest("cursor", "cursor is not valid.")
		}
		params.BeforeAt, params.BeforeID = &at, &id
	}
	out := []Campaign{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListCampaigns(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			last := rows[len(rows)-1]
			c := httpx.EncodeCursor(last.CreatedAt, last.ID)
			next = &c
		}
		out, err = views(r.Context(), q, rows)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

// views adds each campaign's template and recipient counts.
func views(ctx context.Context, q *dbq.Queries, rows []dbq.Campaign) ([]Campaign, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, c := range rows {
		ids[i] = c.ID
	}
	stats, err := q.CampaignStats(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]Stats{}
	for _, s := range stats {
		byID[s.CampaignID] = Stats{Total: s.Total, Pending: s.Pending, Skipped: s.Skipped, Queued: s.Queued,
			Sent: s.Sent, Delivered: s.Delivered, Read: s.Read, Failed: s.Failed}
	}
	tpls := map[uuid.UUID]dbq.Template{}
	out := make([]Campaign, 0, len(rows))
	for _, c := range rows {
		t, ok := tpls[c.TemplateID]
		if !ok {
			if t, err = q.GetTemplate(ctx, c.TemplateID); err != nil && !db.IsNotFound(err) {
				return nil, err
			}
			tpls[c.TemplateID] = t
		}
		out = append(out, view(c, t, byID[c.ID]))
	}
	return out, nil
}

func (s *Service) one(ctx context.Context, q *dbq.Queries, p auth.Principal, id uuid.UUID, lock bool) (dbq.Campaign, error) {
	get := q.GetCampaign
	if lock {
		get = q.GetCampaignForUpdate
	}
	c, err := get(ctx, id)
	if db.IsNotFound(err) || (err == nil && !p.AllowsNumber(c.PhoneNumberID)) {
		return c, notFound()
	}
	return c, err
}

func campaignID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, notFound()
	}
	return id, nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := campaignID(r)
	if err != nil {
		return err
	}
	var out []Campaign
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		c, err := s.one(r.Context(), q, p, id, false)
		if err != nil {
			return err
		}
		out, err = views(r.Context(), q, []dbq.Campaign{c})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out[0])
	return nil
}

// cancel stops a scheduled or running campaign. Recipients not yet queued are skipped; messages
// already queued or sent are not recalled.
func (s *Service) cancel(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := campaignID(r)
	if err != nil {
		return err
	}
	var out []Campaign
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		c, err := s.one(r.Context(), q, p, id, true)
		if err != nil {
			return err
		}
		switch c.Status {
		case dbq.CampaignStatusScheduled, dbq.CampaignStatusRunning, dbq.CampaignStatusPaused:
		default:
			return httpx.NewError(http.StatusConflict, "conflict", "This campaign has already "+string(c.Status)+".")
		}
		if err := q.SkipPendingRecipients(r.Context(), dbq.SkipPendingRecipientsParams{CampaignID: id, Reason: ptr("cancelled")}); err != nil {
			return err
		}
		if err := q.FinishCampaign(r.Context(), dbq.FinishCampaignParams{ID: id, Status: dbq.CampaignStatusCancelled}); err != nil {
			return err
		}
		if c, err = q.GetCampaign(r.Context(), id); err != nil {
			return err
		}
		out, err = views(r.Context(), q, []dbq.Campaign{c})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out[0])
	return nil
}

// AudienceCounts is the dashboard's preview of who a campaign would reach.
type AudienceCounts struct {
	Total    int32 `json:"total"`
	Eligible int32 `json:"eligible"`
	Blocked  int32 `json:"blocked"`
	OptedOut int32 `json:"opted_out"`
	NoOptIn  int32 `json:"no_opt_in"`
}

func (s *Service) audience(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var a Audience
	if err := httpx.Decode(r, &a); err != nil {
		return err
	}
	if err := a.clean(); err != nil {
		return err
	}
	ids, tags := a.params()
	var out AudienceCounts
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		c, err := q.AudienceCounts(r.Context(), dbq.AudienceCountsParams{ContactIds: ids, Tags: tags})
		out = AudienceCounts(c)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type Recipient struct {
	ContactID  uuid.UUID  `json:"contact_id"`
	WaID       string     `json:"wa_id"`
	Name       *string    `json:"name"`
	Status     string     `json:"status"`
	SkipReason *string    `json:"skip_reason"`
	MessageID  *uuid.UUID `json:"message_id"`
	ErrorCode  *int32     `json:"error_code"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (s *Service) recipients(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := campaignID(r)
	if err != nil {
		return err
	}
	qs := r.URL.Query()
	lim, err := httpx.Limit(qs.Get("limit"))
	if err != nil {
		return err
	}
	params := dbq.ListCampaignRecipientsParams{CampaignID: id, Lim: lim + 1}
	if v := qs.Get("status"); v != "" {
		st := dbq.RecipientStatus(v)
		if !st.Valid() {
			return httpx.BadRequest("status", "status must be pending, skipped, queued, sent, delivered, read or failed.")
		}
		params.Status = &st
	}
	if v := qs.Get("cursor"); v != "" {
		after, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("cursor", "cursor is not valid.")
		}
		params.AfterID = &after
	}
	out := []Recipient{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := s.one(r.Context(), q, p, id, false); err != nil {
			return err
		}
		rows, err := q.ListCampaignRecipients(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			c := rows[len(rows)-1].ContactID.String()
			next = &c
		}
		for _, x := range rows {
			out = append(out, Recipient{ContactID: x.ContactID, WaID: x.WaID, Name: x.ContactName, Status: string(x.Status),
				SkipReason: x.SkipReason, MessageID: x.MessageID, ErrorCode: x.ErrorCode, UpdatedAt: x.UpdatedAt})
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

func ptr[T any](v T) *T { return &v }
