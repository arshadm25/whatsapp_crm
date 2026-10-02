package contacts

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
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

// Service serves D6: /v1/contacts, and the dashboard's tags, consent history and CSV import.
type Service struct {
	db  *db.DB
	log *slog.Logger
}

func NewService(d *db.DB, log *slog.Logger) *Service { return &Service{db: d, log: log} }

func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Post("/", httpx.Handler(s.log, s.upsert))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Patch("/{id}", httpx.Handler(s.log, s.patch))
}

func (s *Service) InternalRoutes(r chi.Router) {
	r.Get("/tags", httpx.Handler(s.log, s.tags))
	r.Get("/{id}/consent", httpx.Handler(s.log, s.consentHistory))
	r.With(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin)).Post("/import", httpx.Handler(s.log, s.importCSV))
}

var (
	waIDRE     = regexp.MustCompile(`^[0-9]{6,15}$`)
	languageRE = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
)

// Consent is a consent change: who agreed (or withdrew), how and when.
type Consent struct {
	Status     string     `json:"status"`
	Source     string     `json:"source"`
	Evidence   string     `json:"evidence"`
	OccurredAt *time.Time `json:"occurred_at"`
}

func (c *Consent) check(param string) error {
	if c.Status != "opted_in" && c.Status != "opted_out" {
		return httpx.BadRequest(param+".status", "status must be opted_in or opted_out.")
	}
	switch c.Source {
	case "api", "dashboard", "csv_import", "whatsapp_flow":
	default:
		return httpx.BadRequest(param+".source", "source must be api, dashboard, csv_import or whatsapp_flow.")
	}
	if utf8.RuneCountInString(c.Evidence) > 500 {
		return httpx.BadRequest(param+".evidence", "evidence must be at most 500 characters.")
	}
	if c.OccurredAt != nil && c.OccurredAt.After(time.Now().Add(5*time.Minute)) {
		return httpx.BadRequest(param+".occurred_at", "occurred_at cannot be in the future.")
	}
	return nil
}

// RecordConsent sets a contact's opt-in status and appends the consent record that proves it.
func RecordConsent(ctx context.Context, q *dbq.Queries, tenantID, contactID uuid.UUID, status dbq.OptInStatus,
	source dbq.ConsentSource, evidence string, by *uuid.UUID, at time.Time) (dbq.Contact, error) {
	c, err := q.SetContactConsent(ctx, dbq.SetContactConsentParams{ID: contactID, Status: status, At: at})
	if err != nil {
		return c, err
	}
	kind := dbq.ConsentKindOptIn
	if status == dbq.OptInStatusOptedOut {
		kind = dbq.ConsentKindOptOut
	}
	var ev *string
	if evidence != "" {
		ev = &evidence
	}
	err = q.InsertConsentEvent(ctx, dbq.InsertConsentEventParams{
		TenantID: tenantID, ContactID: contactID, Kind: kind, Source: source, Evidence: ev, RecordedBy: by, OccurredAt: at,
	})
	return c, err
}

func (s *Service) consent(ctx context.Context, q *dbq.Queries, p auth.Principal, contactID uuid.UUID, c *Consent) (dbq.Contact, error) {
	at := time.Now().UTC()
	if c.OccurredAt != nil {
		at = *c.OccurredAt
	}
	return RecordConsent(ctx, q, p.TenantID, contactID, dbq.OptInStatus(c.Status), dbq.ConsentSource(c.Source), c.Evidence, p.User(), at)
}

// cleanTags trims tag names and drops empty and repeated ones.
func cleanTags(param string, in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		if utf8.RuneCountInString(t) > 50 {
			return nil, httpx.BadRequest(param, "Tag names must be at most 50 characters.")
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) > 50 {
		return nil, httpx.BadRequest(param, "Send at most 50 tags.")
	}
	return out, nil
}

func addTags(ctx context.Context, q *dbq.Queries, tenantID, contactID uuid.UUID, names []string) error {
	for _, n := range names {
		id, err := q.UpsertTag(ctx, dbq.UpsertTagParams{TenantID: tenantID, Name: n})
		if err != nil {
			return err
		}
		if err := q.AddContactTag(ctx, dbq.AddContactTagParams{TenantID: tenantID, ContactID: contactID, TagID: id}); err != nil {
			return err
		}
	}
	return nil
}

func checkFields(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil, httpx.BadRequest("custom_fields", "custom_fields must be an object.")
	}
	if len(m) > 50 || len(raw) > 8<<10 {
		return nil, httpx.BadRequest("custom_fields", "custom_fields can have at most 50 fields and 8 KB.")
	}
	for k := range m {
		if k == "" || utf8.RuneCountInString(k) > 64 {
			return nil, httpx.BadRequest("custom_fields", "Field names must have 1 to 64 characters.")
		}
	}
	return bytes.Clone(raw), nil
}

func checkLanguage(l *string) error {
	if l != nil && *l != "" && !languageRE.MatchString(*l) {
		return httpx.BadRequest("language", "language must be a code such as en, hi, ta or en_US.")
	}
	return nil
}

func checkName(n *string) error {
	if n != nil && utf8.RuneCountInString(*n) > 200 {
		return httpx.BadRequest("name", "name must be at most 200 characters.")
	}
	return nil
}

func nonEmpty(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	v := strings.TrimSpace(*s)
	return &v
}

// view loads a contact's tags and renders it.
func view(ctx context.Context, q *dbq.Queries, c dbq.Contact) (Contact, error) {
	rows, err := q.ListContactTags(ctx, []uuid.UUID{c.ID})
	if err != nil {
		return Contact{}, err
	}
	return View(c, Tags(rows)[c.ID]), nil
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	lim, err := httpx.Limit(qs.Get("limit"))
	if err != nil {
		return err
	}
	params := dbq.ListContactsParams{Lim: lim + 1}
	if v := qs.Get("opt_in_status"); v != "" {
		st := dbq.OptInStatus(v)
		if !st.Valid() {
			return httpx.BadRequest("opt_in_status", "opt_in_status must be unknown, opted_in or opted_out.")
		}
		params.OptInStatus = &st
	}
	if v := strings.TrimSpace(qs.Get("tag")); v != "" {
		params.Tag = &v
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		v = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimPrefix(v, "+"))
		params.Search = &v
	}
	if v := qs.Get("cursor"); v != "" {
		at, id, ok := httpx.DecodeCursor(v)
		if !ok {
			return httpx.BadRequest("cursor", "cursor is not valid.")
		}
		params.BeforeAt, params.BeforeID = &at, &id
	}
	out := []Contact{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListContacts(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			last := rows[len(rows)-1]
			c := httpx.EncodeCursor(last.CreatedAt, last.ID)
			next = &c
		}
		ids := make([]uuid.UUID, len(rows))
		for i, c := range rows {
			ids[i] = c.ID
		}
		tagRows, err := q.ListContactTags(r.Context(), ids)
		if err != nil {
			return err
		}
		tags := Tags(tagRows)
		for _, c := range rows {
			out = append(out, View(c, tags[c.ID]))
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

// Input matches ContactInput in api/openapi.yaml.
type Input struct {
	WaID         string          `json:"wa_id"`
	Name         *string         `json:"name"`
	Language     *string         `json:"language"`
	CustomFields json.RawMessage `json:"custom_fields"`
	Tags         []string        `json:"tags"`
	OptIn        *Consent        `json:"opt_in"`
}

func (s *Service) upsert(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.WaID = strings.TrimPrefix(strings.TrimSpace(in.WaID), "+")
	if !waIDRE.MatchString(in.WaID) {
		return httpx.BadRequest("wa_id", "wa_id must be the number in international format, digits only, for example 919876543210.")
	}
	if err := checkName(in.Name); err != nil {
		return err
	}
	if err := checkLanguage(in.Language); err != nil {
		return err
	}
	fields, err := checkFields(in.CustomFields)
	if err != nil {
		return err
	}
	tags, err := cleanTags("tags", in.Tags)
	if err != nil {
		return err
	}
	if in.OptIn != nil {
		if err := in.OptIn.check("opt_in"); err != nil {
			return err
		}
	}
	var (
		out     Contact
		created bool
	)
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		row, err := q.UpsertContact(r.Context(), dbq.UpsertContactParams{
			ID: db.NewID(), TenantID: p.TenantID, WaID: in.WaID, Name: nonEmpty(in.Name), Language: nonEmpty(in.Language), CustomFields: fields,
		})
		if err != nil {
			return err
		}
		created = row.Inserted
		if err := addTags(r.Context(), q, p.TenantID, row.ID, tags); err != nil {
			return err
		}
		c, err := q.GetContact(r.Context(), row.ID)
		if err != nil {
			return err
		}
		if in.OptIn != nil {
			if c, err = s.consent(r.Context(), q, p, c.ID, in.OptIn); err != nil {
				return err
			}
		}
		out, err = view(r.Context(), q, c)
		return err
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, out)
	return nil
}

func contactID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := contactID(r)
	if err != nil {
		return err
	}
	var out Contact
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		c, err := q.GetContact(r.Context(), id)
		if err != nil {
			return err
		}
		out, err = view(r.Context(), q, c)
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

// Patch matches ContactPatch in api/openapi.yaml. An empty name or language clears it.
type Patch struct {
	Name         *string         `json:"name"`
	Language     *string         `json:"language"`
	CustomFields json.RawMessage `json:"custom_fields"`
	AddTags      []string        `json:"add_tags"`
	RemoveTags   []string        `json:"remove_tags"`
	Blocked      *bool           `json:"blocked"`
	Consent      *Consent        `json:"consent"`
}

func (s *Service) patch(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := contactID(r)
	if err != nil {
		return err
	}
	var in Patch
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := checkName(in.Name); err != nil {
		return err
	}
	if err := checkLanguage(in.Language); err != nil {
		return err
	}
	fields, err := checkFields(in.CustomFields)
	if err != nil {
		return err
	}
	add, err := cleanTags("add_tags", in.AddTags)
	if err != nil {
		return err
	}
	remove, err := cleanTags("remove_tags", in.RemoveTags)
	if err != nil {
		return err
	}
	if in.Consent != nil {
		if err := in.Consent.check("consent"); err != nil {
			return err
		}
	}
	var out Contact
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		c, err := q.UpdateContact(r.Context(), dbq.UpdateContactParams{
			ID: id, SetName: in.Name != nil, Name: nonEmpty(in.Name), SetLanguage: in.Language != nil, Language: nonEmpty(in.Language),
			CustomFields: fields, Blocked: in.Blocked,
		})
		if err != nil {
			return err
		}
		for _, n := range remove {
			if err := q.RemoveContactTag(r.Context(), dbq.RemoveContactTagParams{ContactID: id, Name: n}); err != nil {
				return err
			}
		}
		if err := addTags(r.Context(), q, p.TenantID, id, add); err != nil {
			return err
		}
		if in.Consent != nil {
			if c, err = s.consent(r.Context(), q, p, id, in.Consent); err != nil {
				return err
			}
		}
		out, err = view(r.Context(), q, c)
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

// Tag is a tag with the number of contacts that have it.
type Tag struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Contacts int64     `json:"contacts"`
}

func (s *Service) tags(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []Tag{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListTags(r.Context())
		for _, t := range rows {
			out = append(out, Tag{ID: t.ID, Name: t.Name, Contacts: t.Contacts})
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// ConsentEvent is one entry of a contact's consent history.
type ConsentEvent struct {
	Kind           string    `json:"kind"`
	Source         string    `json:"source"`
	Evidence       *string   `json:"evidence"`
	RecordedByName *string   `json:"recorded_by_name"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (s *Service) consentHistory(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := contactID(r)
	if err != nil {
		return err
	}
	out := []ConsentEvent{}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := q.GetContact(r.Context(), id); err != nil {
			return err
		}
		rows, err := q.ListConsentEvents(r.Context(), id)
		for _, e := range rows {
			out = append(out, ConsentEvent{Kind: string(e.Kind), Source: string(e.Source), Evidence: e.Evidence,
				RecordedByName: e.RecordedByName, OccurredAt: e.OccurredAt})
		}
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}
