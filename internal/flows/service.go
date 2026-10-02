package flows

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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

// Meta is the part of the Graph client Flows need.
type Meta interface {
	CreateFlow(ctx context.Context, token, wabaID, name string, categories []string) (*metaclient.CreatedFlow, error)
	UploadFlowJSON(ctx context.Context, token, flowID string, flowJSON []byte) ([]metaclient.FlowError, error)
	GetFlow(ctx context.Context, token, flowID string) (*metaclient.FlowInfo, error)
	UpdateFlow(ctx context.Context, token, flowID string, body map[string]any) error
	PublishFlow(ctx context.Context, token, flowID string) error
	DeprecateFlow(ctx context.Context, token, flowID string) error
	DeleteFlow(ctx context.Context, token, flowID string) error
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

var manage = auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin)

// Routes mounts /v1/flows. Every member can read Flows; owners and admins change them.
func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Group(func(r chi.Router) {
		r.Use(manage)
		r.Post("/", httpx.Handler(s.log, s.create))
		r.Put("/{id}", httpx.Handler(s.log, s.update))
		r.Post("/{id}/refresh", httpx.Handler(s.log, s.refresh))
		r.Post("/{id}/publish", httpx.Handler(s.log, s.publish))
		r.Post("/{id}/deprecate", httpx.Handler(s.log, s.deprecate))
		r.Delete("/{id}", httpx.Handler(s.log, s.remove))
	})
}

// SubmissionRoutes mounts /v1/flow-submissions.
func (s *Service) SubmissionRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.submissions))
}

// Flow matches the Flow schema in api/openapi.yaml.
type Flow struct {
	ID                uuid.UUID       `json:"id"`
	WhatsappAccountID uuid.UUID       `json:"whatsapp_account_id"`
	MetaFlowID        string          `json:"meta_flow_id"`
	Name              string          `json:"name"`
	Categories        []string        `json:"categories"`
	Status            string          `json:"status"`
	FlowJSON          json.RawMessage `json:"flow_json"`
	ValidationErrors  json.RawMessage `json:"validation_errors"`
	PreviewURL        *string         `json:"preview_url"`
	PreviewExpiresAt  *time.Time      `json:"preview_expires_at"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	PublishedAt       *time.Time      `json:"published_at"`
}

func view(f dbq.Flow) Flow {
	return Flow{
		ID: f.ID, WhatsappAccountID: f.WhatsappAccountID, MetaFlowID: f.MetaFlowID, Name: f.Name, Categories: f.Categories,
		Status: f.Status, FlowJSON: f.FlowJson, ValidationErrors: f.ValidationErrors, PreviewURL: f.PreviewUrl,
		PreviewExpiresAt: f.PreviewExpiresAt, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt, PublishedAt: f.PublishedAt,
	}
}

func flowID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []Flow{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListFlows(r.Context())
		for _, f := range rows {
			out = append(out, view(f))
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
	id, err := flowID(r)
	if err != nil {
		return err
	}
	var out Flow
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		f, err := q.GetFlow(r.Context(), id)
		out = view(f)
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

type flowRequest struct {
	WhatsappAccountID uuid.UUID       `json:"whatsapp_account_id"`
	Name              string          `json:"name"`
	Categories        []string        `json:"categories"`
	FlowJSON          json.RawMessage `json:"flow_json"`
}

func (req *flowRequest) check() error {
	req.Name = strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(req.Name); n < 1 || n > 200 {
		return httpx.BadRequest("name", "name must have 1 to 200 characters.")
	}
	if len(req.Categories) == 0 {
		req.Categories = []string{"OTHER"}
	}
	for _, c := range req.Categories {
		if !slices.Contains(Categories, c) {
			return httpx.BadRequest("categories", "Unknown category "+c+". Use "+strings.Join(Categories, ", ")+".")
		}
	}
	if len(req.FlowJSON) > 0 {
		if err := ValidFlowJSON(req.FlowJSON); err != nil {
			return httpx.BadRequest("flow_json", err.Error()+".")
		}
	}
	return nil
}

// accountToken returns the connected account and its decrypted access token.
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

// loadWithToken reads a Flow and the token of its account.
func (s *Service) loadWithToken(ctx context.Context, tenantID, id uuid.UUID) (dbq.Flow, string, error) {
	var (
		f     dbq.Flow
		token string
	)
	err := s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if f, err = q.GetFlow(ctx, id); err != nil {
			return err
		}
		acct, err := q.GetWhatsAppAccount(ctx, f.WhatsappAccountID)
		if err != nil {
			return err
		}
		if acct.Status != dbq.ConnectionStatusConnected {
			return errNotConnected
		}
		token, err = credentials.Token(ctx, q, s.keys, acct)
		return err
	})
	if db.IsNotFound(err) {
		return f, "", httpx.ErrNotFound
	}
	return f, token, err
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req flowRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if err := req.check(); err != nil {
		return err
	}
	if len(req.FlowJSON) == 0 {
		req.FlowJSON = json.RawMessage(StarterJSON)
	}
	acct, token, err := s.accountToken(r.Context(), p.TenantID, req.WhatsappAccountID)
	if err != nil {
		return err
	}
	ctx := metaclient.WithTenant(r.Context(), p.TenantID.String())
	created, err := s.meta.CreateFlow(ctx, token, acct.WabaID, req.Name, req.Categories)
	if err != nil {
		return metaError(err)
	}
	errs, err := s.meta.UploadFlowJSON(ctx, token, created.ID, req.FlowJSON)
	if err != nil {
		return metaError(err)
	}
	info, _ := s.meta.GetFlow(ctx, token, created.ID) // the preview is optional
	var out Flow
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		url, exp := preview(info)
		f, err := q.InsertFlow(r.Context(), dbq.InsertFlowParams{
			ID: db.NewID(), TenantID: p.TenantID, WhatsappAccountID: acct.ID, MetaFlowID: created.ID, Name: req.Name,
			Categories: req.Categories, Status: "draft", FlowJson: req.FlowJSON, ValidationErrors: errorsJSON(errs),
			PreviewUrl: url, PreviewExpiresAt: exp, CreatedBy: p.User(),
		})
		out = view(f)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

// update saves a draft: the new JSON goes to Meta, which validates it and returns a preview.
// A draft with validation errors is saved and cannot be published until they are fixed.
func (s *Service) update(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := flowID(r)
	if err != nil {
		return err
	}
	var req flowRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if err := req.check(); err != nil {
		return err
	}
	f, token, err := s.loadWithToken(r.Context(), p.TenantID, id)
	if err != nil {
		return err
	}
	if f.Status != "draft" {
		return httpx.NewError(http.StatusConflict, "conflict", "Only a draft can be edited. Create a new Flow to change a published one.")
	}
	if len(req.FlowJSON) == 0 {
		req.FlowJSON = f.FlowJson
	}
	ctx := metaclient.WithTenant(r.Context(), p.TenantID.String())
	if req.Name != f.Name || !slices.Equal(req.Categories, f.Categories) {
		if err := s.meta.UpdateFlow(ctx, token, f.MetaFlowID, map[string]any{"name": req.Name, "categories": req.Categories}); err != nil {
			return metaError(err)
		}
	}
	errs, err := s.meta.UploadFlowJSON(ctx, token, f.MetaFlowID, req.FlowJSON)
	if err != nil {
		return metaError(err)
	}
	info, _ := s.meta.GetFlow(ctx, token, f.MetaFlowID)
	var out Flow
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		url, exp := preview(info)
		if url == nil {
			url, exp = f.PreviewUrl, f.PreviewExpiresAt
		}
		u, err := q.UpdateFlowContent(r.Context(), dbq.UpdateFlowContentParams{
			ID: id, Name: req.Name, Categories: req.Categories, FlowJson: req.FlowJSON, ValidationErrors: errorsJSON(errs),
			PreviewUrl: url, PreviewExpiresAt: exp,
		})
		out = view(u)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// refresh reads the Flow's status, validation errors and a fresh preview link from Meta.
func (s *Service) refresh(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := flowID(r)
	if err != nil {
		return err
	}
	f, token, err := s.loadWithToken(r.Context(), p.TenantID, id)
	if err != nil {
		return err
	}
	info, err := s.meta.GetFlow(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, f.MetaFlowID)
	if err != nil {
		return metaError(err)
	}
	st, ok := MetaStatus(info.Status)
	if !ok {
		st = f.Status
	}
	return s.saveStatus(w, r, p, id, st, info)
}

func (s *Service) saveStatus(w http.ResponseWriter, r *http.Request, p auth.Principal, id uuid.UUID, status string, info *metaclient.FlowInfo) error {
	var out Flow
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		params := dbq.UpdateFlowStatusParams{ID: id, Status: status}
		if info != nil {
			params.ValidationErrors = errorsJSON(info.ValidationErrors)
			params.PreviewUrl, params.PreviewExpiresAt = preview(info)
		}
		f, err := q.UpdateFlowStatus(r.Context(), params)
		out = view(f)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) publish(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := flowID(r)
	if err != nil {
		return err
	}
	f, token, err := s.loadWithToken(r.Context(), p.TenantID, id)
	if err != nil {
		return err
	}
	if f.Status != "draft" {
		return httpx.NewError(http.StatusConflict, "conflict", "Only a draft can be published.")
	}
	if string(f.ValidationErrors) != "[]" && len(f.ValidationErrors) > 0 {
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "flow_invalid", Message: "Fix the validation errors before publishing."}
	}
	if err := s.meta.PublishFlow(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, f.MetaFlowID); err != nil {
		return metaError(err)
	}
	return s.saveStatus(w, r, p, id, "published", nil)
}

func (s *Service) deprecate(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := flowID(r)
	if err != nil {
		return err
	}
	f, token, err := s.loadWithToken(r.Context(), p.TenantID, id)
	if err != nil {
		return err
	}
	if f.Status != "published" {
		return httpx.NewError(http.StatusConflict, "conflict", "Only a published Flow can be deprecated.")
	}
	if err := s.meta.DeprecateFlow(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, f.MetaFlowID); err != nil {
		return metaError(err)
	}
	return s.saveStatus(w, r, p, id, "deprecated", nil)
}

func (s *Service) remove(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := flowID(r)
	if err != nil {
		return err
	}
	f, token, err := s.loadWithToken(r.Context(), p.TenantID, id)
	if err != nil {
		return err
	}
	if f.Status != "draft" {
		return httpx.NewError(http.StatusConflict, "conflict", "Only a draft can be deleted. Deprecate a published Flow instead.")
	}
	if err := s.meta.DeleteFlow(metaclient.WithTenant(r.Context(), p.TenantID.String()), token, f.MetaFlowID); err != nil {
		var me *metaclient.Error
		if !errors.As(err, &me) || me.HTTPStatus != http.StatusNotFound {
			return metaError(err)
		}
	}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.DeleteFlow(r.Context(), id)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) submissions(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	lim, err := httpx.Limit(qs.Get("limit"))
	if err != nil {
		return err
	}
	params := dbq.ListFlowSubmissionsParams{Lim: lim + 1}
	for name, dst := range map[string]**uuid.UUID{"flow_id": &params.FlowID, "contact_id": &params.ContactID} {
		if v := qs.Get(name); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				return httpx.BadRequest(name, name+" must be a UUID.")
			}
			*dst = &id
		}
	}
	if v := qs.Get("cursor"); v != "" {
		at, id, ok := httpx.DecodeCursor(v)
		if !ok {
			return httpx.BadRequest("cursor", "Invalid cursor.")
		}
		params.BeforeAt, params.BeforeID = &at, &id
	}
	out := []Submission{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListFlowSubmissions(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			last := rows[len(rows)-1].FlowSubmission
			c := httpx.EncodeCursor(last.CreatedAt, last.ID)
			next = &c
		}
		for _, x := range rows {
			out = append(out, submissionView(x.FlowSubmission, x.ContactWaID, x.ContactName, x.FlowName))
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

func errorsJSON(errs []metaclient.FlowError) []byte {
	if len(errs) == 0 {
		return []byte("[]")
	}
	b, _ := json.Marshal(errs)
	return b
}

func preview(info *metaclient.FlowInfo) (*string, *time.Time) {
	if info == nil || info.Preview == nil || info.Preview.PreviewURL == "" {
		return nil, nil
	}
	u := info.Preview.PreviewURL
	var exp *time.Time
	if t, err := time.Parse(time.RFC3339, info.Preview.ExpiresAt); err == nil {
		exp = &t
	}
	return &u, exp
}

func metaError(err error) error {
	var me *metaclient.Error
	if !errors.As(err, &me) {
		return err
	}
	if me.IsTokenInvalid() {
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "number_not_connected", MetaErrorCode: me.Code,
			Message: "Meta no longer accepts this account's access. Reconnect WhatsApp from Numbers."}
	}
	status := http.StatusUnprocessableEntity
	if me.HTTPStatus >= 500 || me.Retryable() {
		status = http.StatusBadGateway
	}
	return &httpx.Error{Status: status, Code: "meta_error", Message: me.Friendly(), MetaErrorCode: me.Code}
}
