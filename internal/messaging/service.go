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
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/media"
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
	MediaID         *uuid.UUID      `json:"media_id"`
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
		Origin: string(m.Origin), Type: string(m.Type), Content: m.Content, MediaID: m.MediaID, Status: string(m.Status),
		Error: errorView(m.ErrorCode, m.ErrorTitle), CampaignID: m.CampaignID,
		CreatedAt: m.CreatedAt, StatusUpdatedAt: m.StatusUpdatedAt,
	}
	if m.PricingCategory != nil {
		v.Pricing = &Pricing{Category: *m.PricingCategory, Billable: m.PricingBillable != nil && *m.PricingBillable}
	}
	return v
}

var waIDRE = regexp.MustCompile(`^[0-9]{6,15}$`)

// Replay is the stored response of the first request that used an Idempotency-Key.
type Replay struct {
	Status int
	Body   []byte
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
		cached *Replay
	)
	if p.Sandbox {
		return s.sendSandbox(w, r, p, &req, content)
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		if err := billing.Check(ctx, q, s.now()); err != nil {
			return err
		}
		if key != "" {
			hash := sha256.Sum256(raw)
			cached, err = ClaimIdempotencyKey(ctx, q, p.TenantID, key, hash[:], s.now())
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
		WriteReplay(w, cached)
		return nil
	}
	httpx.JSON(w, http.StatusAccepted, out)
	return nil
}

var errSandboxRollback = errors.New("sandbox send is not stored")

// sendSandbox runs a send through every check a live send faces, then rolls the work back, so
// a sandbox key gets a realistic response while nothing is stored, queued or sent to Meta.
func (s *Service) sendSandbox(w http.ResponseWriter, r *http.Request, p auth.Principal, req *SendRequest, content map[string]any) error {
	var out Message
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		msg, err := s.queue(r.Context(), q, tx, p, req, content, "")
		if err != nil {
			return err
		}
		out = msg
		return errSandboxRollback
	})
	if !errors.Is(err, errSandboxRollback) {
		return err
	}
	wamid := "wamid.SANDBOX" + strings.ReplaceAll(out.ID.String(), "-", "")
	out.Wamid, out.Status = &wamid, "sent"
	w.Header().Set("Ecogo-Sandbox", "true")
	httpx.JSON(w, http.StatusAccepted, out)
	return nil
}

// ClaimIdempotencyKey records a new Idempotency-Key, or returns the stored response of the first
// request that used it. The same key with a different body is refused. Keys are shared by every
// endpoint that takes one (message sends, campaign creation).
func ClaimIdempotencyKey(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, key string, hash []byte, now time.Time) (*Replay, error) {
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
	if now.Sub(rec.CreatedAt) > idempotencyTTL {
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
	return &Replay{Status: int(*rec.ResponseStatus), Body: rec.ResponseBody}, nil
}

// WriteReplay sends a stored response again, marked as replayed.
func WriteReplay(w http.ResponseWriter, r *Replay) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(r.Status)
	_, _ = w.Write(r.Body)
}

// queue runs the business checks and stores the message with its send job.
func (s *Service) queue(ctx context.Context, q *dbq.Queries, tx pgx.Tx, p auth.Principal, req *SendRequest, content map[string]any, key string) (Message, error) {
	num, err := q.GetSendingNumber(ctx, req.PhoneNumberID)
	if db.IsNotFound(err) || (err == nil && !p.AllowsNumber(num.PhoneNumber.ID)) {
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

	mediaID, err := attachedMedia(ctx, q, req)
	if err != nil {
		return Message{}, err
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
		ContactID: contact.ID, Origin: origin(p), Type: dbq.MessageType(req.Type), Content: body,
		TemplateID: templateID, ReplyToWamid: replyWamid(content), SentByUserID: p.User(), IdempotencyKey: idemKey,
		MediaID: mediaID,
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

// origin records whether a message came from the dashboard or the public API.
func origin(p auth.Principal) dbq.MessageOrigin {
	if p.IsAPIKey() {
		return dbq.MessageOriginApi
	}
	return dbq.MessageOriginDashboard
}

// attachedMedia checks an uploaded file named by media_id exists and suits the message type.
func attachedMedia(ctx context.Context, q *dbq.Queries, req *SendRequest) (*uuid.UUID, error) {
	ref := map[string]*MediaRef{"image": req.Image, "video": req.Video, "audio": req.Audio, "document": req.Document, "sticker": req.Sticker}[req.Type]
	if ref == nil || ref.MediaID == nil {
		return nil, nil
	}
	m, err := q.GetMedia(ctx, *ref.MediaID)
	if db.IsNotFound(err) {
		return nil, unprocessable("invalid_request", req.Type+".media_id", "There is no uploaded file with this media_id.")
	}
	if err != nil {
		return nil, err
	}
	if !media.Fits(req.Type, m.MimeType) {
		return nil, unprocessable("invalid_request", req.Type+".media_id", "A "+m.MimeType+" file cannot be sent as "+req.Type+".")
	}
	if _, limit, _ := media.KindOf(m.MimeType); req.Type != media.KindDocument && m.SizeBytes > limit {
		return nil, unprocessable("media_too_large", req.Type+".media_id", "This file is too large to send as "+req.Type+".")
	}
	return &m.ID, nil
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
		if json.Unmarshal(req.Interactive, &head) != nil || (head.Type != "button" && head.Type != "list" && head.Type != "cta_url" && head.Type != "flow" && head.Type != "carousel") {
			return nil, httpx.BadRequest("interactive.type", "interactive.type must be button, list, cta_url, flow or carousel.")
		}
		if head.Type == "carousel" {
			if err := checkCarousel(req.Interactive); err != nil {
				return nil, err
			}
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
	var obj map[string]any
	switch {
	case m.MediaID != nil && m.Link != "":
		return nil, httpx.BadRequest(typ, "Send either media_id or link, not both.")
	case m.MediaID != nil:
		// The worker swaps our media ID for Meta's when it sends.
		obj = map[string]any{"media_id": m.MediaID.String()}
	default:
		u, err := url.Parse(m.Link)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, httpx.BadRequest(typ+".link", "link must be a public HTTPS URL, or upload the file with POST /v1/media and send its media_id.")
		}
		obj = map[string]any{"link": m.Link}
	}
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
	var header, body, cards int
	for _, raw := range comps {
		var c struct {
			Type       string            `json:"type"`
			Parameters []json.RawMessage `json:"parameters"`
			Cards      []json.RawMessage `json:"cards"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return httpx.BadRequest("template.components", "Each component must be an object with a type and parameters.")
		}
		switch strings.ToLower(c.Type) {
		case "header":
			header += len(c.Parameters)
		case "body":
			body += len(c.Parameters)
		case "carousel":
			cards += len(c.Cards)
		}
	}
	if cards != shape.Cards {
		if shape.Cards == 0 {
			return unprocessable("template_param_mismatch", "template.components", "This template is not a carousel; remove the carousel component.")
		}
		// Meta needs every card, with its header media, on each send.
		return unprocessable("template_param_mismatch", "template.components",
			"This carousel template has "+itoa(shape.Cards)+" cards; send a carousel component with "+itoa(shape.Cards)+" cards, the same number it was approved with.")
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

// checkCarousel validates an interactive media carousel against Meta's limits: 2 to 10 cards,
// a required main body, no main header, footer or buttons, an image or video header on each
// card, and the same button type and count on every card. Card media goes to Meta as is, so a
// header takes a public link or a Meta media id, not one of our media_id values.
func checkCarousel(raw json.RawMessage) error {
	type button struct {
		Type       string `json:"type"`
		QuickReply *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"quick_reply"`
	}
	type mediaObj struct {
		ID      string `json:"id"`
		Link    string `json:"link"`
		MediaID string `json:"media_id"`
	}
	var c struct {
		Header json.RawMessage `json:"header"`
		Footer json.RawMessage `json:"footer"`
		Body   *struct {
			Text string `json:"text"`
		} `json:"body"`
		Action struct {
			Buttons json.RawMessage `json:"buttons"`
			Cards   []struct {
				CardIndex *int   `json:"card_index"`
				Type      string `json:"type"`
				Header    struct {
					Type  string    `json:"type"`
					Image *mediaObj `json:"image"`
					Video *mediaObj `json:"video"`
				} `json:"header"`
				Body *struct {
					Text string `json:"text"`
				} `json:"body"`
				Action struct {
					Name       string `json:"name"`
					Parameters *struct {
						DisplayText string `json:"display_text"`
						URL         string `json:"url"`
					} `json:"parameters"`
					Buttons []button `json:"buttons"`
				} `json:"action"`
			} `json:"cards"`
		} `json:"action"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return httpx.BadRequest("interactive", "interactive must be a carousel object with body and action.cards.")
	}
	if len(c.Header) > 0 || len(c.Footer) > 0 || len(c.Action.Buttons) > 0 {
		return httpx.BadRequest("interactive", "A carousel has no main header, footer or buttons; put media and buttons on the cards.")
	}
	if c.Body == nil || strings.TrimSpace(c.Body.Text) == "" || utf8.RuneCountInString(c.Body.Text) > 1024 {
		return httpx.BadRequest("interactive.body.text", "A carousel needs body text of 1 to 1024 characters.")
	}
	cards := c.Action.Cards
	if len(cards) < 2 || len(cards) > 10 {
		return httpx.BadRequest("interactive.action.cards", "A carousel needs 2 to 10 cards.")
	}
	var kind string
	var count int
	for i, card := range cards {
		p := "interactive.action.cards[" + strconv.Itoa(i) + "]"
		if card.CardIndex == nil || *card.CardIndex != i {
			return httpx.BadRequest(p+".card_index", "card_index must count up from 0 in card order.")
		}
		var m *mediaObj
		switch card.Header.Type {
		case "image":
			m = card.Header.Image
		case "video":
			m = card.Header.Video
		default:
			return httpx.BadRequest(p+".header.type", "Each card needs an image or video header.")
		}
		switch {
		case m == nil || (m.ID == "" && m.Link == ""):
			return httpx.BadRequest(p+".header", "The card header needs a link or a Meta media id.")
		case m.MediaID != "":
			return httpx.BadRequest(p+".header", "Carousel cards cannot use media_id from POST /v1/media yet; send a public HTTPS link.")
		case m.Link != "":
			if u, err := url.Parse(m.Link); err != nil || u.Scheme != "https" || u.Host == "" {
				return httpx.BadRequest(p+".header.link", "link must be a public HTTPS URL.")
			}
		}
		if card.Body != nil {
			if n := utf8.RuneCountInString(card.Body.Text); n > 160 || strings.Count(card.Body.Text, "\n") > 2 {
				return httpx.BadRequest(p+".body.text", "Card body text can have at most 160 characters and 2 line breaks.")
			}
		}
		k, n := "", 0
		switch {
		case card.Action.Name == "cta_url" && len(card.Action.Buttons) == 0:
			ps := card.Action.Parameters
			if ps == nil || ps.DisplayText == "" || utf8.RuneCountInString(ps.DisplayText) > 20 {
				return httpx.BadRequest(p+".action.parameters.display_text", "The URL button needs a label of 1 to 20 characters.")
			}
			if u, err := url.Parse(ps.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return httpx.BadRequest(p+".action.parameters.url", "The URL button needs a valid web address.")
			}
			k, n = "cta_url", 1
		case card.Action.Name == "" && len(card.Action.Buttons) > 0:
			for j, b := range card.Action.Buttons {
				bp := p + ".action.buttons[" + strconv.Itoa(j) + "]"
				if b.Type != "quick_reply" || b.QuickReply == nil {
					return httpx.BadRequest(bp, "Card buttons must be quick_reply buttons, or use one cta_url action.")
				}
				if b.QuickReply.ID == "" || len(b.QuickReply.ID) > 256 {
					return httpx.BadRequest(bp+".quick_reply.id", "The quick reply id must have 1 to 256 characters.")
				}
				if b.QuickReply.Title == "" || utf8.RuneCountInString(b.QuickReply.Title) > 20 {
					return httpx.BadRequest(bp+".quick_reply.title", "The quick reply title must have 1 to 20 characters.")
				}
			}
			k, n = "quick_reply", len(card.Action.Buttons)
		default:
			return httpx.BadRequest(p+".action", "Each card needs either a cta_url action or quick_reply buttons.")
		}
		if i == 0 {
			kind, count = k, n
		} else if k != kind || n != count {
			return httpx.BadRequest(p+".action", "Every card must have the same button type and number of buttons.")
		}
	}
	return nil
}
