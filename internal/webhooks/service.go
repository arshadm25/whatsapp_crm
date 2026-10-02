package webhooks

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

// Service serves /v1/webhook-endpoints.
type Service struct {
	db       *db.DB
	keys     *envelope.Keyring
	inserter jobs.Inserter
	log      *slog.Logger
}

func NewService(d *db.DB, keys *envelope.Keyring, inserter jobs.Inserter, log *slog.Logger) *Service {
	return &Service{db: d, keys: keys, inserter: inserter, log: log}
}

func (s *Service) Routes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin, dbq.MemberRoleDeveloper))
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Post("/", httpx.Handler(s.log, s.create))
	r.Delete("/{id}", httpx.Handler(s.log, s.delete))
	r.Get("/{id}/deliveries", httpx.Handler(s.log, s.deliveries))
	r.Get("/{id}/deliveries/{delivery_id}", httpx.Handler(s.log, s.delivery))
	r.Post("/{id}/deliveries/{delivery_id}/retry", httpx.Handler(s.log, s.retry))
	r.Post("/{id}/secret", httpx.Handler(s.log, s.revealSecret))
	r.Post("/{id}/secret/rotate", httpx.Handler(s.log, s.rotateSecret))
}

// Endpoint matches the WebhookEndpoint schema in api/openapi.yaml.
type Endpoint struct {
	ID            uuid.UUID  `json:"id"`
	URL           string     `json:"url"`
	Description   *string    `json:"description"`
	EventTypes    []string   `json:"event_types"`
	PhoneNumberID *uuid.UUID `json:"phone_number_id"`
	Enabled       bool       `json:"enabled"`
	CreatedAt     time.Time  `json:"created_at"`
}

func endpointView(e dbq.WebhookEndpoint) Endpoint {
	return Endpoint{ID: e.ID, URL: e.Url, Description: e.Description, EventTypes: e.EventTypes,
		PhoneNumberID: e.PhoneNumberID, Enabled: e.IsEnabled, CreatedAt: e.CreatedAt}
}

// Delivery matches the WebhookDelivery schema.
type Delivery struct {
	ID               uuid.UUID  `json:"id"`
	EventID          uuid.UUID  `json:"event_id"`
	EventType        string     `json:"event_type"`
	Status           string     `json:"status"`
	AttemptCount     int32      `json:"attempt_count"`
	LastResponseCode *int32     `json:"last_response_code"`
	LastError        *string    `json:"last_error"`
	NextAttemptAt    *time.Time `json:"next_attempt_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

func deliveryView(d dbq.WebhookDelivery) Delivery {
	v := Delivery{ID: d.ID, EventID: d.EventID, EventType: d.EventType, Status: string(d.Status),
		AttemptCount: d.AttemptCount, LastResponseCode: d.LastResponseCode, LastError: d.LastError, CreatedAt: d.CreatedAt}
	if d.Status == dbq.DeliveryStatusPending || d.Status == dbq.DeliveryStatusRetrying {
		v.NextAttemptAt = &d.NextAttemptAt
	}
	return v
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []Endpoint{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListWebhookEndpoints(r.Context())
		for _, e := range rows {
			if visible(p, e) {
				out = append(out, endpointView(e))
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// CreatedEndpoint carries the signing secret, returned only when the endpoint is created.
type CreatedEndpoint struct {
	Endpoint
	Secret string `json:"secret"`
}

// newSecret makes a signing secret and seals it for the endpoint.
func (s *Service) newSecret(tenantID, endpointID uuid.UUID) (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	secret := "whsec_" + base64.RawURLEncoding.EncodeToString(raw)
	sealed, err := s.keys.SealCompact([]byte(secret), SecretAAD(tenantID, endpointID))
	return secret, sealed, err
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		URL           string     `json:"url"`
		Description   string     `json:"description"`
		EventTypes    []string   `json:"event_types"`
		PhoneNumberID *uuid.UUID `json:"phone_number_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(req.URL) > 2000 {
		return httpx.BadRequest("url", "url must be an https:// address without a username or password.")
	}
	if utf8.RuneCountInString(req.Description) > 200 {
		return httpx.BadRequest("description", "description must be at most 200 characters.")
	}
	if len(req.EventTypes) == 0 {
		return httpx.BadRequest("event_types", "Choose at least one event type.")
	}
	types := []string{}
	for _, t := range req.EventTypes {
		if !slices.Contains(EventTypes, t) {
			return httpx.BadRequest("event_types", "Unknown event type "+t+". Use "+strings.Join(EventTypes, ", ")+".")
		}
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	if p.KeyPhoneNumberID != nil {
		if req.PhoneNumberID == nil {
			req.PhoneNumberID = p.KeyPhoneNumberID
		} else if !p.AllowsNumber(*req.PhoneNumberID) {
			return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Param: "phone_number_id", Message: "Phone number not found."}
		}
	}

	id := db.NewID()
	secret, sealed, err := s.newSecret(p.TenantID, id)
	if err != nil {
		return err
	}
	var desc *string
	if d := strings.TrimSpace(req.Description); d != "" {
		desc = &d
	}
	var e dbq.WebhookEndpoint
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if req.PhoneNumberID != nil {
			if _, err := q.GetSendingNumber(r.Context(), *req.PhoneNumberID); err != nil {
				if db.IsNotFound(err) {
					return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Param: "phone_number_id", Message: "Phone number not found."}
				}
				return err
			}
		}
		e, err = q.InsertWebhookEndpoint(r.Context(), dbq.InsertWebhookEndpointParams{
			ID: id, TenantID: p.TenantID, Url: u.String(), Description: desc, SecretCiphertext: sealed,
			EventTypes: types, PhoneNumberID: req.PhoneNumberID,
		})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, CreatedEndpoint{Endpoint: endpointView(e), Secret: secret})
	return nil
}

// endpoint loads an endpoint the caller may see.
func endpoint(r *http.Request, q *dbq.Queries, p auth.Principal) (dbq.WebhookEndpoint, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return dbq.WebhookEndpoint{}, httpx.ErrNotFound
	}
	e, err := q.GetWebhookEndpoint(r.Context(), id)
	if db.IsNotFound(err) || (err == nil && !visible(p, e)) {
		return e, httpx.ErrNotFound
	}
	return e, err
}

// visible reports whether a key limited to one number may see an endpoint: only that number's.
func visible(p auth.Principal, e dbq.WebhookEndpoint) bool {
	return p.KeyPhoneNumberID == nil || (e.PhoneNumberID != nil && *e.PhoneNumberID == *p.KeyPhoneNumberID)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		e, err := endpoint(r, q, p)
		if err != nil {
			return err
		}
		_, err = q.DeleteWebhookEndpoint(r.Context(), e.ID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) deliveries(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	lim, err := httpx.Limit(qs.Get("limit"))
	if err != nil {
		return err
	}
	params := dbq.ListDeliveriesParams{Lim: lim + 1}
	if v := qs.Get("status"); v != "" {
		st := dbq.DeliveryStatus(v)
		if !st.Valid() {
			return httpx.BadRequest("status", "status must be pending, succeeded, retrying or dead.")
		}
		params.Status = &st
	}
	if v := qs.Get("cursor"); v != "" {
		at, id, ok := httpx.DecodeCursor(v)
		if !ok {
			return httpx.BadRequest("cursor", "cursor is not valid.")
		}
		params.BeforeAt, params.BeforeID = &at, &id
	}
	out := []Delivery{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		e, err := endpoint(r, q, p)
		if err != nil {
			return err
		}
		params.EndpointID = e.ID
		rows, err := q.ListDeliveries(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			last := rows[len(rows)-1]
			c := httpx.EncodeCursor(last.CreatedAt, last.ID)
			next = &c
		}
		for _, d := range rows {
			out = append(out, deliveryView(d))
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

func (s *Service) retry(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	deliveryID, err := uuid.Parse(chi.URLParam(r, "delivery_id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		e, err := endpoint(r, q, p)
		if err != nil {
			return err
		}
		n, err := q.RequeueDelivery(r.Context(), dbq.RequeueDeliveryParams{ID: deliveryID, EndpointID: e.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return httpx.ErrNotFound
		}
		_, err = s.inserter.InsertTx(r.Context(), tx, DeliverArgs{TenantID: p.TenantID, DeliveryID: deliveryID}, nil)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// DeliveryDetail is a delivery with the payload that was sent.
type DeliveryDetail struct {
	Delivery
	Payload json.RawMessage `json:"payload"`
}

func (s *Service) delivery(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	deliveryID, err := uuid.Parse(chi.URLParam(r, "delivery_id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var out DeliveryDetail
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		e, err := endpoint(r, q, p)
		if err != nil {
			return err
		}
		d, err := q.GetDelivery(r.Context(), dbq.GetDeliveryParams{ID: deliveryID, EndpointID: e.ID})
		if db.IsNotFound(err) {
			return httpx.ErrNotFound
		}
		out = DeliveryDetail{Delivery: deliveryView(d), Payload: d.Payload}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// revealSecret returns an endpoint's signing secret again. It is a POST so the secret is never
// cached or logged as a URL.
func (s *Service) revealSecret(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var secret []byte
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		e, err := endpoint(r, q, p)
		if err != nil {
			return err
		}
		secret, err = s.keys.OpenCompact(e.SecretCiphertext, SecretAAD(p.TenantID, e.ID))
		return err
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]string{"secret": string(secret)})
	return nil
}

// rotateSecret replaces an endpoint's signing secret; deliveries from now on are signed with
// the new one.
func (s *Service) rotateSecret(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var secret string
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		e, err := endpoint(r, q, p)
		if err != nil {
			return err
		}
		var sealed []byte
		if secret, sealed, err = s.newSecret(p.TenantID, e.ID); err != nil {
			return err
		}
		return q.SetWebhookSecret(r.Context(), dbq.SetWebhookSecretParams{ID: e.ID, SecretCiphertext: sealed})
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]string{"secret": secret})
	return nil
}
