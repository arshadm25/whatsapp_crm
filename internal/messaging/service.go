// Package messaging sends WhatsApp messages (design Flow 3). POST /v1/messages checks the
// request, the customer's opt-out and the 24-hour window, then stores the message as queued and
// enqueues its send job in one transaction. The send worker calls Meta and records the wamid
// or the mapped error; delivery statuses arrive later through internal/metaevents.
package messaging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
)

// Meta is the part of the Graph client messaging needs.
type Meta interface {
	SendMessage(ctx context.Context, token, phoneNumberID, to string, body map[string]any) (string, error)
	MarkRead(ctx context.Context, token, phoneNumberID, wamid string, typing bool) error
}

// Window is how long after a customer's last message free-form messages are allowed.
const Window = 24 * time.Hour

// idempotencyTTL is how long an Idempotency-Key replays its first response.
const idempotencyTTL = 24 * time.Hour

type Service struct {
	db       *db.DB
	keys     *envelope.Keyring
	meta     Meta
	inserter jobs.Inserter
	log      *slog.Logger
	now      func() time.Time
}

func NewService(d *db.DB, keys *envelope.Keyring, meta Meta, inserter jobs.Inserter, log *slog.Logger) *Service {
	return &Service{db: d, keys: keys, meta: meta, inserter: inserter, log: log, now: time.Now}
}

// Routes mounts /v1/messages.
func (s *Service) Routes(r chi.Router) {
	r.Post("/", httpx.Handler(s.log, s.send))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Post("/{id}/read", httpx.Handler(s.log, s.markRead))
}

// SendRequest matches SendMessageRequest in api/openapi.yaml.
type SendRequest struct {
	PhoneNumberID uuid.UUID       `json:"phone_number_id"`
	To            string          `json:"to"`
	Type          string          `json:"type"`
	ReplyTo       *uuid.UUID      `json:"reply_to,omitempty"`
	Text          *TextBody       `json:"text,omitempty"`
	Image         *MediaRef       `json:"image,omitempty"`
	Video         *MediaRef       `json:"video,omitempty"`
	Audio         *MediaRef       `json:"audio,omitempty"`
	Document      *MediaRef       `json:"document,omitempty"`
	Sticker       *MediaRef       `json:"sticker,omitempty"`
	Location      *Location       `json:"location,omitempty"`
	Template      *TemplateRef    `json:"template,omitempty"`
	Interactive   json.RawMessage `json:"interactive,omitempty"`
	Reaction      *Reaction       `json:"reaction,omitempty"`
}

type TextBody struct {
	Body       string `json:"body"`
	PreviewURL bool   `json:"preview_url,omitempty"`
}

type MediaRef struct {
	MediaID  *uuid.UUID `json:"media_id,omitempty"`
	Link     string     `json:"link,omitempty"`
	Caption  string     `json:"caption,omitempty"`
	Filename string     `json:"filename,omitempty"` // documents only
}

type Location struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Name      string   `json:"name,omitempty"`
	Address   string   `json:"address,omitempty"`
}

type TemplateRef struct {
	Name       string            `json:"name"`
	Language   string            `json:"language"`
	Components []json.RawMessage `json:"components,omitempty"`
}

type Reaction struct {
	MessageID uuid.UUID `json:"message_id"`
	Emoji     *string   `json:"emoji"`
}

// Message matches the Message schema in api/openapi.yaml.
type Message struct {
	ID              uuid.UUID       `json:"id"`
	Wamid           *string         `json:"wamid"`
	ConversationID  uuid.UUID       `json:"conversation_id"`
	PhoneNumberID   uuid.UUID       `json:"phone_number_id"`
	Contact         ContactRef      `json:"contact"`
	Direction       string          `json:"direction"`
	Origin          string          `json:"origin"`
	Type            string          `json:"type"`
	Content         json.RawMessage `json:"content"`
	Status          string          `json:"status"`
	Error           *ErrorView      `json:"error"`
	Pricing         *Pricing        `json:"pricing"`
	CampaignID      *uuid.UUID      `json:"campaign_id"`
	CreatedAt       time.Time       `json:"created_at"`
	StatusUpdatedAt time.Time       `json:"status_updated_at"`
}

type ContactRef struct {
	ID   uuid.UUID `json:"id"`
	WaID string    `json:"wa_id"`
	Name *string   `json:"name"`
}

type Pricing struct {
	Category string `json:"category"`
	Billable bool   `json:"billable"`
}

// View renders a stored message in the API shape.
func View(m dbq.Message, waID string, name *string) Message {
	v := Message{
		ID: m.ID, Wamid: m.Wamid, ConversationID: m.ConversationID, PhoneNumberID: m.PhoneNumberID,
		Contact: ContactRef{ID: m.ContactID, WaID: waID, Name: name}, Direction: string(m.Direction),
		Origin: string(m.Origin), Type: string(m.Type), Content: m.Content, Status: string(m.Status),
		Error: errorView(m.ErrorCode, m.ErrorTitle), CampaignID: m.CampaignID,
		CreatedAt: m.CreatedAt, StatusUpdatedAt: m.StatusUpdatedAt,
	}
	if m.PricingCategory != nil {
		v.Pricing = &Pricing{Category: *m.PricingCategory, Billable: m.PricingBillable != nil && *m.PricingBillable}
	}
	return v
}

var waIDRE = regexp.MustCompile(`^[0-9]{6,15}$`)

// replay is a stored response for a repeated Idempotency-Key.
type replay struct {
	status int
	body   []byte
}

func (s *Service) send(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return httpx.BadRequest("", "Could not read the request body.")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var req SendRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 255 {
		return httpx.BadRequest("Idempotency-Key", "Idempotency-Key must be at most 255 characters.")
	}
	content, err := buildContent(&req)
	if err != nil {
		return err
	}

	var (
		out    Message
		cached *replay
	)
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		if key != "" {
			hash := sha256.Sum256(raw)
			cached, err = s.claimKey(ctx, q, p.TenantID, key, hash[:])
			if err != nil || cached != nil {
				return err
			}
		}
		msg, err := s.queue(ctx, q, tx, p, &req, content, key)
		if err != nil {
			return err
		}
		out = msg
		if key != "" {
			body, _ := json.Marshal(out)
			status := int32(http.StatusAccepted)
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
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(cached.status)
		_, _ = w.Write(cached.body)
		return nil
	}
	httpx.JSON(w, http.StatusAccepted, out)
	return nil
}

// claimKey records a new Idempotency-Key, or returns the stored response of the first request
// that used it. The same key with a different body is refused.
func (s *Service) claimKey(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, key string, hash []byte) (*replay, error) {
	_, err := q.ClaimIdempotencyKey(ctx, dbq.ClaimIdempotencyKeyParams{TenantID: tenantID, Key: key, RequestHash: hash})
	if err == nil {
		return nil, nil
	}
	if !db.IsNotFound(err) {
		return nil, err
	}
	rec, err := q.GetIdempotencyRecordForUpdate(ctx, dbq.GetIdempotencyRecordForUpdateParams{TenantID: tenantID, Key: key})
	if err != nil {
		return nil, err
	}
	if s.now().Sub(rec.CreatedAt) > idempotencyTTL {
		if err := q.RestartIdempotencyRecord(ctx, dbq.RestartIdempotencyRecordParams{TenantID: tenantID, Key: key, RequestHash: hash}); err != nil {
			return nil, err
		}
		return nil, q.ClearIdempotencyKey(ctx, dbq.ClearIdempotencyKeyParams{TenantID: tenantID, IdempotencyKey: &key})
	}
	if !bytes.Equal(rec.RequestHash, hash) {
		return nil, unprocessable("idempotency_conflict", "Idempotency-Key",
			"This Idempotency-Key was already used with a different request body.")
	}
	if rec.ResponseStatus == nil {
		return nil, httpx.NewError(http.StatusConflict, "conflict", "A request with this Idempotency-Key is still being processed.")
	}
	return &replay{status: int(*rec.ResponseStatus), body: rec.ResponseBody}, nil
}

// queue runs the business checks and stores the message with its send job.
func (s *Service) queue(ctx context.Context, q *dbq.Queries, tx pgx.Tx, p auth.Principal, req *SendRequest, content map[string]any, key string) (Message, error) {
	num, err := q.GetSendingNumber(ctx, req.PhoneNumberID)
	if db.IsNotFound(err) {
		return Message{}, &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Param: "phone_number_id", Message: "Phone number not found."}
	}
	if err != nil {
		return Message{}, err
	}
	if num.PhoneNumber.Status != dbq.ConnectionStatusConnected || num.WhatsappAccount.Status != dbq.ConnectionStatusConnected {
		return Message{}, unprocessable("number_not_connected", "phone_number_id", "This number is not connected. Reconnect it from Numbers.")
	}

	contact, err := q.UpsertContactFromWhatsApp(ctx, dbq.UpsertContactFromWhatsAppParams{ID: db.NewID(), TenantID: p.TenantID, WaID: req.To})
	if err != nil {
		return Message{}, err
	}
	if contact.Blocked {
		return Message{}, unprocessable("contact_blocked", "to", "This contact is blocked.")
	}
	if contact.OptInStatus == dbq.OptInStatusOptedOut {
		return Message{}, unprocessable("contact_opted_out", "to", "This contact has opted out of messages from you.")
	}
	conv, err := q.UpsertConversation(ctx, dbq.UpsertConversationParams{
		ID: db.NewID(), TenantID: p.TenantID, PhoneNumberID: num.PhoneNumber.ID, ContactID: contact.ID,
	})
	if err != nil {
		return Message{}, err
	}
	now := s.now()
	if req.Type != "template" && (conv.LastInboundAt == nil || now.Sub(*conv.LastInboundAt) >= Window) {
		return Message{}, unprocessable("window_closed", "type",
			"The customer has not written in the last 24 hours, so only an approved template can be sent.")
	}

	// Quoted replies and reactions point at one of our messages; Meta needs its wamid.
	if req.ReplyTo != nil {
		wamid, err := s.wamidOf(ctx, q, *req.ReplyTo, conv.ID, "reply_to")
		if err != nil {
			return Message{}, err
		}
		content["context"] = map[string]string{"message_id": wamid}
	}
	if req.Reaction != nil {
		wamid, err := s.wamidOf(ctx, q, req.Reaction.MessageID, conv.ID, "reaction.message_id")
		if err != nil {
			return Message{}, err
		}
		content["reaction"] = map[string]string{"message_id": wamid, "emoji": *req.Reaction.Emoji}
	}

	var templateID *uuid.UUID
	if req.Template != nil {
		t, err := q.GetTemplateByName(ctx, dbq.GetTemplateByNameParams{
			WhatsappAccountID: num.WhatsappAccount.ID, Name: req.Template.Name, Language: req.Template.Language,
		})
		if db.IsNotFound(err) || (err == nil && t.Status != dbq.TemplateStatusApproved) {
			return Message{}, unprocessable("template_not_approved", "template.name",
				"There is no approved template with this name and language on this number's account.")
		}
		if err != nil {
			return Message{}, err
		}
		if err := checkTemplateParams(t, req.Template.Components); err != nil {
			return Message{}, err
		}
		templateID = &t.ID
	}

	body, err := json.Marshal(content)
	if err != nil {
		return Message{}, err
	}
	var idemKey *string
	if key != "" {
		idemKey = &key
	}
	msg, err := q.InsertOutboundMessage(ctx, dbq.InsertOutboundMessageParams{
		ID: db.NewID(), TenantID: p.TenantID, ConversationID: conv.ID, PhoneNumberID: num.PhoneNumber.ID,
		ContactID: contact.ID, Origin: dbq.MessageOriginDashboard, Type: dbq.MessageType(req.Type), Content: body,
		TemplateID: templateID, ReplyToWamid: replyWamid(content), SentByUserID: &p.UserID, IdempotencyKey: idemKey,
	})
	if err != nil {
		return Message{}, err
	}
	if _, err := s.inserter.InsertTx(ctx, tx, SendArgs{MessageID: msg.ID, TenantID: p.TenantID}, nil); err != nil {
		return Message{}, err
	}
	if err := q.TouchConversationOutbound(ctx, dbq.TouchConversationOutboundParams{ID: conv.ID, At: now, Preview: preview(req)}); err != nil {
		return Message{}, err
	}
	return View(msg, contact.WaID, displayName(contact)), nil
}

func (s *Service) wamidOf(ctx context.Context, q *dbq.Queries, id, conversationID uuid.UUID, param string) (string, error) {
	m, err := q.GetMessageByID(ctx, id)
	if db.IsNotFound(err) || (err == nil && (m.ConversationID != conversationID || m.Wamid == nil)) {
		return "", unprocessable("invalid_request", param, "That message is not in this conversation, or has not reached WhatsApp yet.")
	}
	return deref(m.Wamid), err
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var out Message
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		row, err := q.GetMessageView(r.Context(), id)
		out = View(row.Message, row.ContactWaID, row.ContactName)
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

func (s *Service) markRead(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req struct {
		TypingIndicator bool `json:"typing_indicator"`
	}
	if r.ContentLength != 0 {
		if err := httpx.Decode(r, &req); err != nil {
			return err
		}
	}
	var (
		msg   dbq.Message
		num   dbq.GetSendingNumberRow
		token string
	)
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if msg, err = q.GetMessageByID(ctx, id); err != nil {
			return err
		}
		if msg.Direction != dbq.MessageDirectionInbound || msg.Wamid == nil {
			return unprocessable("invalid_request", "message_id", "Only messages from the customer can be marked as read.")
		}
		if num, err = q.GetSendingNumber(ctx, msg.PhoneNumberID); err != nil {
			return err
		}
		if num.WhatsappAccount.Status != dbq.ConnectionStatusConnected {
			return unprocessable("number_not_connected", "", "This number is not connected. Reconnect it from Numbers.")
		}
		token, err = credentials.Token(ctx, q, s.keys, num.WhatsappAccount)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := s.meta.MarkRead(metaclient.WithTenant(ctx, p.TenantID.String()), token, num.PhoneNumber.PhoneNumberID, *msg.Wamid, req.TypingIndicator); err != nil {
		var me *metaclient.Error
		if errors.As(err, &me) {
			v := errorView(ptr(int32(me.Code)), ptr(me.Friendly()))
			return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: v.Code, Message: v.Message, MetaErrorCode: me.Code}
		}
		return err
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.ResetConversationUnread(ctx, msg.ConversationID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// buildContent validates the request shape and returns the Cloud API message object (without
// recipient fields) that the worker sends and the messages table stores.
func buildContent(req *SendRequest) (map[string]any, error) {
	if req.PhoneNumberID == uuid.Nil {
		return nil, httpx.BadRequest("phone_number_id", "phone_number_id is required.")
	}
	if !waIDRE.MatchString(req.To) {
		return nil, httpx.BadRequest("to", "to must be the customer's number in international format, digits only, for example 919876543210.")
	}
	present := map[string]bool{
		"text": req.Text != nil, "image": req.Image != nil, "video": req.Video != nil, "audio": req.Audio != nil,
		"document": req.Document != nil, "sticker": req.Sticker != nil, "location": req.Location != nil,
		"template": req.Template != nil, "interactive": len(req.Interactive) > 0, "reaction": req.Reaction != nil,
	}
	if _, ok := present[req.Type]; !ok {
		return nil, httpx.BadRequest("type", "type must be one of text, image, video, audio, document, sticker, location, template, interactive or reaction.")
	}
	for k, v := range present {
		if v != (k == req.Type) {
			return nil, httpx.BadRequest(k, "Send exactly one message object, matching type.")
		}
	}

	c := map[string]any{"type": req.Type}
	switch req.Type {
	case "text":
		n := utf8.RuneCountInString(req.Text.Body)
		if strings.TrimSpace(req.Text.Body) == "" || n > 4096 {
			return nil, httpx.BadRequest("text.body", "text.body must have 1 to 4096 characters.")
		}
		c["text"] = map[string]any{"body": req.Text.Body, "preview_url": req.Text.PreviewURL}
	case "image", "video", "audio", "document", "sticker":
		m := map[string]*MediaRef{"image": req.Image, "video": req.Video, "audio": req.Audio, "document": req.Document, "sticker": req.Sticker}[req.Type]
		obj, err := mediaObject(req.Type, m)
		if err != nil {
			return nil, err
		}
		c[req.Type] = obj
	case "location":
		l := req.Location
		if l.Latitude == nil || l.Longitude == nil || *l.Latitude < -90 || *l.Latitude > 90 || *l.Longitude < -180 || *l.Longitude > 180 {
			return nil, httpx.BadRequest("location", "location needs a valid latitude and longitude.")
		}
		obj := map[string]any{"latitude": *l.Latitude, "longitude": *l.Longitude}
		if l.Name != "" {
			obj["name"] = l.Name
		}
		if l.Address != "" {
			obj["address"] = l.Address
		}
		c["location"] = obj
	case "template":
		t := req.Template
		if t.Name == "" || t.Language == "" {
			return nil, httpx.BadRequest("template", "template needs a name and a language.")
		}
		obj := map[string]any{"name": t.Name, "language": map[string]string{"code": t.Language}}
		if len(t.Components) > 0 {
			obj["components"] = t.Components
		}
		c["template"] = obj
	case "interactive":
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(req.Interactive, &head) != nil || (head.Type != "button" && head.Type != "list" && head.Type != "cta_url") {
			return nil, httpx.BadRequest("interactive.type", "interactive.type must be button, list or cta_url.")
		}
		c["interactive"] = req.Interactive
	case "reaction":
		if req.Reaction.MessageID == uuid.Nil || req.Reaction.Emoji == nil {
			return nil, httpx.BadRequest("reaction", "reaction needs message_id and emoji (an empty emoji removes a reaction).")
		}
		// The wamid is filled in once the message is found.
	}
	return c, nil
}

func mediaObject(typ string, m *MediaRef) (map[string]any, error) {
	if m.MediaID != nil {
		return nil, unprocessable("invalid_request", typ+".media_id", "Media upload is not available yet. Send a public HTTPS link instead.")
	}
	u, err := url.Parse(m.Link)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, httpx.BadRequest(typ+".link", "link must be a public HTTPS URL.")
	}
	obj := map[string]any{"link": m.Link}
	if m.Caption != "" {
		if typ == "audio" || typ == "sticker" {
			return nil, httpx.BadRequest(typ+".caption", "Audio and stickers cannot have a caption.")
		}
		if utf8.RuneCountInString(m.Caption) > 1024 {
			return nil, httpx.BadRequest(typ+".caption", "caption must be at most 1024 characters.")
		}
		obj["caption"] = m.Caption
	}
	if m.Filename != "" {
		if typ != "document" {
			return nil, httpx.BadRequest(typ+".filename", "Only documents have a filename.")
		}
		obj["filename"] = m.Filename
	}
	return obj, nil
}

// checkTemplateParams compares the variables sent with the ones the template defines, so a
// mismatch fails here with a clear error instead of later at Meta.
func checkTemplateParams(t dbq.Template, comps []json.RawMessage) error {
	shape := templates.ShapeOf(t.Components)
	var header, body int
	for _, raw := range comps {
		var c struct {
			Type       string            `json:"type"`
			Parameters []json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return httpx.BadRequest("template.components", "Each component must be an object with a type and parameters.")
		}
		switch strings.ToLower(c.Type) {
		case "header":
			header += len(c.Parameters)
		case "body":
			body += len(c.Parameters)
		}
	}
	wantHeader := shape.HeaderVars
	switch shape.HeaderFormat {
	case "IMAGE", "VIDEO", "DOCUMENT", "LOCATION":
		wantHeader = 1
	}
	if body != shape.BodyVars || header != wantHeader {
		return unprocessable("template_param_mismatch", "template.components",
			"The template expects "+itoa(wantHeader)+" header and "+itoa(shape.BodyVars)+" body parameters; the request has "+
				itoa(header)+" and "+itoa(body)+".")
	}
	return nil
}

// preview is the conversation list's one-line summary of a sent message.
func preview(req *SendRequest) string {
	var p string
	switch req.Type {
	case "text":
		p = req.Text.Body
	case "template":
		p = "Template: " + req.Template.Name
	case "image", "video", "document":
		m := map[string]*MediaRef{"image": req.Image, "video": req.Video, "document": req.Document}[req.Type]
		p = "[" + req.Type + "] " + m.Caption
	case "reaction":
		p = "Reacted " + deref(req.Reaction.Emoji)
	default:
		p = "[" + req.Type + "]"
	}
	if utf8.RuneCountInString(p) > 100 {
		p = string([]rune(p)[:100])
	}
	return strings.TrimSpace(p)
}

func replyWamid(content map[string]any) *string {
	if ctx, ok := content["context"].(map[string]string); ok {
		id := ctx["message_id"]
		return &id
	}
	return nil
}

func displayName(c dbq.Contact) *string {
	if c.Name != nil {
		return c.Name
	}
	return c.ProfileName
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func ptr[T any](v T) *T { return &v }

func itoa(n int) string { return strconv.Itoa(n) }
