package templates

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

type Service struct {
	db     *db.DB
	keys   *envelope.Keyring
	meta   Meta
	syncer *Syncer
	log    *slog.Logger
}

func NewService(d *db.DB, keys *envelope.Keyring, meta Meta, log *slog.Logger) *Service {
	return &Service{db: d, keys: keys, meta: meta, syncer: NewSyncer(d, meta), log: log}
}

// Routes mounts /v1/templates. Every member can read templates (agents pick them in the inbox);
// only owners and admins create, edit or delete them.
func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
		r.Post("/", httpx.Handler(s.log, s.create))
		r.Patch("/{id}", httpx.Handler(s.log, s.edit))
		r.Delete("/by-name/{name}", httpx.Handler(s.log, s.deleteByName))
	})
}

// InternalRoutes mounts /internal/templates for the dashboard.
func (s *Service) InternalRoutes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
	r.Post("/sync", httpx.Handler(s.log, s.sync))
}

// Template matches the Template schema in api/openapi.yaml.
type Template struct {
	ID                uuid.UUID       `json:"id"`
	MetaTemplateID    *string         `json:"meta_template_id"`
	WhatsappAccountID uuid.UUID       `json:"whatsapp_account_id"`
	Name              string          `json:"name"`
	Language          string          `json:"language"`
	Category          string          `json:"category"`
	Status            string          `json:"status"`
	RejectedReason    *string         `json:"rejected_reason"`
	QualityScore      *string         `json:"quality_score"`
	ParameterFormat   string          `json:"parameter_format"`
	Components        json.RawMessage `json:"components"`
	CreatedAt         time.Time       `json:"created_at"`
	StatusUpdatedAt   *time.Time      `json:"status_updated_at"`
}

func View(t dbq.Template) Template {
	return Template{
		ID: t.ID, MetaTemplateID: t.MetaTemplateID, WhatsappAccountID: t.WhatsappAccountID, Name: t.Name,
		Language: t.Language, Category: string(t.Category), Status: string(t.Status), RejectedReason: t.RejectedReason,
		QualityScore: t.QualityScore, ParameterFormat: t.ParameterFormat, Components: t.Components,
		CreatedAt: t.CreatedAt, StatusUpdatedAt: t.StatusUpdatedAt,
	}
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	params := dbq.ListTemplatesParams{Lim: 25}
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return httpx.BadRequest("limit", "limit must be between 1 and 100.")
		}
		params.Lim = int32(n)
	}
	if v := qs.Get("whatsapp_account_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("whatsapp_account_id", "whatsapp_account_id must be a UUID.")
		}
		params.WhatsappAccountID = &id
	}
	if v := qs.Get("status"); v != "" {
		st := dbq.TemplateStatus(v)
		if !st.Valid() {
			return httpx.BadRequest("status", "Unknown template status.")
		}
		params.Status = &st
	}
	if v := qs.Get("category"); v != "" {
		c := dbq.TemplateCategory(v)
		if !c.Valid() {
			return httpx.BadRequest("category", "category must be marketing, utility or authentication.")
		}
		params.Category = &c
	}
	if v := qs.Get("name"); v != "" {
		params.Name = &v
	}
	if v := qs.Get("cursor"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("cursor", "Invalid cursor.")
		}
		params.Before = &id
	}
	out := []Template{}
	var next *string
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		lim := params.Lim
		params.Lim = lim + 1
		rows, err := q.ListTemplates(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			c := rows[len(rows)-1].ID.String()
			next = &c
		}
		for _, t := range rows {
			out = append(out, View(t))
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var t dbq.Template
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err = q.GetTemplate(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) || (err == nil && t.Status == dbq.TemplateStatusDeleted) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, View(t))
	return nil
}

type createRequest struct {
	WhatsappAccountID uuid.UUID         `json:"whatsapp_account_id"`
	Name              string            `json:"name"`
	Language          string            `json:"language"`
	Category          string            `json:"category"`
	ParameterFormat   string            `json:"parameter_format"`
	Components        []json.RawMessage `json:"components"`
}

var (
	nameRE     = regexp.MustCompile(`^[a-z0-9_]+$`)
	languageRE = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
)

func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req createRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if !nameRE.MatchString(req.Name) || len(req.Name) > 512 {
		return httpx.BadRequest("name", "Use lowercase letters, digits and underscores only, up to 512 characters.")
	}
	if !languageRE.MatchString(req.Language) {
		return httpx.BadRequest("language", "language must be a Meta language code such as en, en_US, hi or ta.")
	}
	cat := dbq.TemplateCategory(req.Category)
	if !cat.Valid() {
		return httpx.BadRequest("category", "category must be marketing, utility or authentication.")
	}
	format := req.ParameterFormat
	if format == "" {
		format = "positional"
	}
	if format != "positional" && format != "named" {
		return httpx.BadRequest("parameter_format", "parameter_format must be positional or named.")
	}
	comps, err := checkComponents(req.Components)
	if err != nil {
		return err
	}

	acct, token, err := s.accountToken(r.Context(), p.TenantID, req.WhatsappAccountID)
	if err != nil {
		return err
	}
	body := map[string]any{
		"name": req.Name, "language": req.Language, "category": strings.ToUpper(req.Category), "components": comps,
	}
	if format == "named" {
		body["parameter_format"] = "NAMED"
	}
	created, err := s.meta.CreateTemplate(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, acct.WabaID, body)
	if err != nil {
		return metaError(err)
	}
	st, ok := StatusFromMeta(created.Status)
	if !ok {
		st = dbq.TemplateStatusPending
	}
	if c, ok := categoryFromMeta(created.Category); ok {
		cat = c // Meta may move a template to the category its content fits
	}
	now := time.Now()
	var t dbq.Template
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err = q.UpsertTemplateFromMeta(r.Context(), dbq.UpsertTemplateFromMetaParams{
			ID: db.NewID(), TenantID: p.TenantID, WhatsappAccountID: acct.ID, MetaTemplateID: &created.ID,
			Name: req.Name, Language: req.Language, Category: cat, Status: st, ParameterFormat: format,
			Components: comps, CreatedBy: p.User(), SubmittedAt: &now,
		})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, View(t))
	return nil
}

type editRequest struct {
	Category   *string           `json:"category"`
	Components []json.RawMessage `json:"components"`
}

func (s *Service) edit(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req editRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if req.Category == nil && req.Components == nil {
		return httpx.BadRequest("", "Send category, components or both.")
	}
	var t dbq.Template
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err = q.GetTemplate(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) || (err == nil && (t.Status == dbq.TemplateStatusDeleted || t.MetaTemplateID == nil)) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	body := map[string]any{}
	cat, comps := t.Category, json.RawMessage(t.Components)
	if req.Category != nil {
		cat = dbq.TemplateCategory(*req.Category)
		if !cat.Valid() {
			return httpx.BadRequest("category", "category must be marketing, utility or authentication.")
		}
		body["category"] = strings.ToUpper(*req.Category)
	}
	if req.Components != nil {
		if comps, err = checkComponents(req.Components); err != nil {
			return err
		}
		body["components"] = comps
	}
	_, token, err := s.accountToken(r.Context(), p.TenantID, t.WhatsappAccountID)
	if err != nil {
		return err
	}
	if err := s.meta.EditTemplate(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, *t.MetaTemplateID, body); err != nil {
		return metaError(err)
	}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err = q.UpdateTemplateAfterEdit(r.Context(), dbq.UpdateTemplateAfterEditParams{ID: id, Category: cat, Components: comps})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, View(t))
	return nil
}

func (s *Service) deleteByName(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	name := chi.URLParam(r, "name")
	accountID, err := uuid.Parse(r.URL.Query().Get("whatsapp_account_id"))
	if err != nil {
		return httpx.BadRequest("whatsapp_account_id", "whatsapp_account_id is required.")
	}
	if !nameRE.MatchString(name) {
		return httpx.ErrNotFound
	}
	acct, token, err := s.accountToken(r.Context(), p.TenantID, accountID)
	if err != nil {
		return err
	}
	if err := s.meta.DeleteTemplate(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, acct.WabaID, name); err != nil {
		var me *metaclient.Error
		if errors.As(err, &me) && me.Code == 100 && me.Subcode == 2593002 {
			return httpx.ErrNotFound // Meta has no template with this name
		}
		return metaError(err)
	}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.DeleteTemplatesByName(r.Context(), dbq.DeleteTemplatesByNameParams{WhatsappAccountID: acct.ID, Name: name})
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sync pulls templates from Meta for every connected account of the tenant.
func (s *Service) sync(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var accts []dbq.WhatsappAccount
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		accts, err = q.ListWhatsAppAccounts(r.Context())
		return err
	})
	if err != nil {
		return err
	}
	total := 0
	for _, a := range accts {
		if a.Status != dbq.ConnectionStatusConnected {
			continue
		}
		_, token, err := s.accountToken(r.Context(), p.TenantID, a.ID)
		if err != nil {
			return err
		}
		n, err := s.syncer.SyncAccount(r.Context(), p.TenantID, a, token)
		if err != nil {
			return metaError(err)
		}
		total += n
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"synced": total})
	return nil
}

// accountToken loads a connected WhatsApp account of the tenant and decrypts its token.
func (s *Service) accountToken(ctx context.Context, tenantID, accountID uuid.UUID) (dbq.WhatsappAccount, string, error) {
	var (
		acct  dbq.WhatsappAccount
		token string
	)
	err := s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if acct, err = q.GetWhatsAppAccount(ctx, accountID); err != nil {
			return err
		}
		if acct.Status != dbq.ConnectionStatusConnected {
			return errNotConnected
		}
		token, err = credentials.Token(ctx, q, s.keys, acct)
		return err
	})
	if db.IsNotFound(err) {
		return acct, "", &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Message: "WhatsApp account not found.", Param: "whatsapp_account_id"}
	}
	return acct, token, err
}

var errNotConnected = httpx.NewError(http.StatusUnprocessableEntity, "number_not_connected",
	"This WhatsApp account is not connected. Reconnect it from Numbers first.")

// checkComponents does the checks Meta's errors explain poorly; Meta validates the rest.
func checkComponents(raw []json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, httpx.BadRequest("components", "A template needs at least a BODY component.")
	}
	bodies := 0
	for i, c := range raw {
		var head struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(c, &head); err != nil {
			return nil, httpx.BadRequest("components["+strconv.Itoa(i)+"]", "Each component must be an object with a type.")
		}
		switch strings.ToUpper(head.Type) {
		case "BODY":
			bodies++
			if strings.TrimSpace(head.Text) == "" {
				return nil, httpx.BadRequest("components["+strconv.Itoa(i)+"].text", "The body text is empty.")
			}
		case "HEADER", "FOOTER", "BUTTONS":
		default:
			return nil, httpx.BadRequest("components["+strconv.Itoa(i)+"].type", "type must be HEADER, BODY, FOOTER or BUTTONS.")
		}
	}
	if bodies != 1 {
		return nil, httpx.BadRequest("components", "A template needs exactly one BODY component.")
	}
	b, err := json.Marshal(raw)
	return b, err
}

// metaError turns a failed Graph call into an API error that carries Meta's explanation.
func metaError(err error) error {
	var me *metaclient.Error
	if !errors.As(err, &me) {
		return err
	}
	if me.IsTokenInvalid() {
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "number_not_connected", MetaErrorCode: me.Code,
			Message: "Meta no longer accepts this account's access. Reconnect WhatsApp from Numbers."}
	}
	if me.Subcode == 2388024 {
		return &httpx.Error{Status: http.StatusConflict, Code: "conflict", MetaErrorCode: me.Code,
			Message: "A template with this name and language already exists."}
	}
	status := http.StatusUnprocessableEntity
	if me.HTTPStatus >= 500 || me.Retryable() {
		status = http.StatusBadGateway
	}
	return &httpx.Error{Status: status, Code: "meta_error", Message: me.Friendly(), MetaErrorCode: me.Code}
}
