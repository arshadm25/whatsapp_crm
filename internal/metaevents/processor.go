package metaevents

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/media"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

// Processor is the River worker that applies one webhook delivery.
type Processor struct {
	river.WorkerDefaults[ProcessArgs]
	db  *db.DB
	log *slog.Logger
	// Jobs enqueues follow-up jobs; when nil, the River client working the job is used.
	Jobs jobs.Inserter
}

func NewProcessor(d *db.DB, log *slog.Logger) *Processor {
	return &Processor{db: d, log: log}
}

func (p *Processor) Work(ctx context.Context, job *river.Job[ProcessArgs]) error {
	a := job.Args
	hash, err := hex.DecodeString(a.PayloadHash)
	if err != nil || len(hash) != 32 {
		return river.JobCancel(fmt.Errorf("meta event: bad payload hash"))
	}
	var payload Payload
	parseErr := json.Unmarshal(a.Payload, &payload)

	eventID, done, err := p.record(ctx, hash, a, payload)
	if err != nil {
		return err
	}
	if done {
		return nil // a replayed delivery that was already applied
	}
	if parseErr != nil {
		return p.finish(ctx, eventID, "unparseable payload: "+parseErr.Error())
	}

	var errs []string
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if err := p.apply(ctx, entry, change); err != nil {
				if job.Attempt < job.MaxAttempts {
					return err // retried; every apply step is idempotent
				}
				errs = append(errs, change.Field+": "+err.Error())
			}
		}
	}
	return p.finish(ctx, eventID, strings.Join(errs, "; "))
}

// record stores the raw delivery once, keyed by payload hash. done reports a delivery that
// was already fully processed.
func (p *Processor) record(ctx context.Context, hash []byte, a ProcessArgs, payload Payload) (id int64, done bool, err error) {
	var field, wabaID, phoneID *string
	if len(payload.Entry) > 0 {
		e := payload.Entry[0]
		wabaID = nonEmpty(e.ID)
		if len(e.Changes) > 0 {
			field = nonEmpty(e.Changes[0].Field)
			var meta struct {
				Metadata struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
			}
			_ = json.Unmarshal(e.Changes[0].Value, &meta)
			phoneID = nonEmpty(meta.Metadata.PhoneNumberID)
		}
	}
	object := payload.Object
	if object == "" {
		object = "unknown"
	}
	err = p.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var tenant *uuid.UUID
		if t, err := q.RouteMetaEvent(ctx, dbq.RouteMetaEventParams{PhoneNumberID: deref(phoneID), WabaID: deref(wabaID)}); err != nil {
			return err
		} else if t != uuid.Nil {
			tenant = &t
		}
		id, err = q.InsertMetaWebhookEvent(ctx, dbq.InsertMetaWebhookEventParams{
			PayloadHash: hash, ReceivedAt: a.ReceivedAt, Object: object, Field: field,
			WabaID: wabaID, MetaPhoneNumberID: phoneID, TenantID: tenant, Payload: storedPayload(a.Payload),
		})
		if db.IsNotFound(err) {
			prev, err := q.GetMetaWebhookEventByHash(ctx, hash)
			if err != nil {
				return err
			}
			id, done = prev.ID, prev.ProcessedAt != nil
			return nil
		}
		return err
	})
	return id, done, err
}

func (p *Processor) finish(ctx context.Context, id int64, errText string) error {
	return p.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.FinishMetaWebhookEvent(ctx, dbq.FinishMetaWebhookEventParams{ID: id, Error: nonEmpty(errText)})
	})
}

func (p *Processor) apply(ctx context.Context, entry Entry, change Change) error {
	switch change.Field {
	case "messages":
		return p.messages(ctx, entry, change.Value, false)
	case "smb_message_echoes":
		return p.messages(ctx, entry, change.Value, true)
	case "message_template_status_update":
		return p.templateStatus(ctx, entry, change.Value)
	case "phone_number_quality_update":
		return p.quality(ctx, entry, change.Value)
	case "account_update":
		return p.accountUpdate(ctx, entry, change.Value)
	case "smb_app_state_sync":
		return p.stateSync(ctx, entry, change.Value)
	default:
		// history (coexistence chat import) and other fields are stored in meta_webhook_events
		// and applied by later slices.
		p.log.Info("meta event: field not handled yet", "field", change.Field, "waba_id", entry.ID)
		return nil
	}
}

// inTenant routes a change to its tenant and runs fn there. Events for numbers or accounts we
// do not know (for example a client that disconnected) are dropped.
func (p *Processor) inTenant(ctx context.Context, phoneNumberID, wabaID string, fn func(q *dbq.Queries, tx pgx.Tx, tenantID uuid.UUID) error) error {
	var tenantID uuid.UUID
	err := p.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		tenantID, err = q.RouteMetaEvent(ctx, dbq.RouteMetaEventParams{PhoneNumberID: phoneNumberID, WabaID: wabaID})
		return err
	})
	if err != nil {
		return err
	}
	if tenantID == uuid.Nil {
		p.log.Warn("meta event: no tenant for event", "phone_number_id", phoneNumberID, "waba_id", wabaID)
		return nil
	}
	return p.db.InTenant(ctx, tenantID, func(q *dbq.Queries, tx pgx.Tx) error { return fn(q, tx, tenantID) })
}

// messages handles inbound messages and statuses ("messages"), and messages the business sent
// from the WhatsApp Business app on a coexistence number ("smb_message_echoes").
func (p *Processor) messages(ctx context.Context, entry Entry, raw json.RawMessage, echoes bool) error {
	var v MessagesValue
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil // malformed value: kept in meta_webhook_events, nothing to apply
	}
	return p.inTenant(ctx, v.Metadata.PhoneNumberID, entry.ID, func(q *dbq.Queries, tx pgx.Tx, tenantID uuid.UUID) error {
		phone, err := q.GetPhoneNumberByMetaID(ctx, v.Metadata.PhoneNumberID)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		names := map[string]string{}
		for _, c := range v.Contacts {
			names[c.WaID] = c.Profile.Name
		}
		msgs := v.Messages
		if echoes {
			msgs = v.MessageEchoes
		}
		for _, m := range msgs {
			if err := p.storeMessage(ctx, q, tx, tenantID, phone, m, names, echoes); err != nil {
				return err
			}
		}
		for _, s := range v.Statuses {
			if err := p.applyStatus(ctx, q, tx, tenantID, phone, s); err != nil {
				return err
			}
		}
		return nil
	})
}

var waID = regexp.MustCompile(`^[0-9]{6,15}$`)

func (p *Processor) storeMessage(ctx context.Context, q *dbq.Queries, tx pgx.Tx, tenantID uuid.UUID, phone dbq.PhoneNumber,
	raw json.RawMessage, names map[string]string, echo bool) error {
	var h MessageHeader
	if err := json.Unmarshal(raw, &h); err != nil || h.ID == "" {
		return nil
	}
	customer := h.From
	direction, origin, status := dbq.MessageDirectionInbound, dbq.MessageOriginCustomer, dbq.MessageStatusReceived
	if echo {
		customer = h.To
		direction, origin, status = dbq.MessageDirectionOutbound, dbq.MessageOriginPhoneApp, dbq.MessageStatusSent
	}
	if !waID.MatchString(customer) {
		p.log.Warn("meta event: message with unusable customer number", "wamid", h.ID)
		return nil
	}
	at := unixTime(h.Timestamp, time.Now().UTC())

	contact, err := q.UpsertContactFromWhatsApp(ctx, dbq.UpsertContactFromWhatsAppParams{
		ID: db.NewID(), TenantID: tenantID, WaID: customer, ProfileName: nonEmpty(names[customer]),
	})
	if err != nil {
		return err
	}
	conv, err := q.UpsertConversation(ctx, dbq.UpsertConversationParams{
		ID: db.NewID(), TenantID: tenantID, PhoneNumberID: phone.ID, ContactID: contact.ID,
	})
	if err != nil {
		return err
	}
	typ := dbq.MessageType(h.Type)
	if !typ.Valid() {
		typ = dbq.MessageTypeUnsupported
	}
	var replyTo *string
	if h.Context != nil {
		replyTo = nonEmpty(h.Context.ID)
	}
	msgID, err := q.InsertWhatsAppMessage(ctx, dbq.InsertWhatsAppMessageParams{
		ID: db.NewID(), TenantID: tenantID, ConversationID: conv.ID, PhoneNumberID: phone.ID, ContactID: contact.ID,
		Direction: direction, Origin: origin, Wamid: &h.ID, Type: typ, Content: raw,
		ReplyToWamid: replyTo, Status: status, MetaTimestamp: &at,
	})
	if db.IsNotFound(err) {
		return nil // already stored (Meta re-sent the message)
	}
	if err != nil {
		return err
	}
	if err := p.queueMediaDownload(ctx, tx, tenantID, msgID, h); err != nil {
		return err
	}
	if err := messaging.EmitEvent(ctx, q, tx, p.Jobs, tenantID, msgID, webhooks.MessageReceived); err != nil {
		return err
	}
	if !echo && h.Text != nil {
		if err := applyKeyword(ctx, q, tenantID, contact, h.Text.Body, at); err != nil {
			return err
		}
	}
	if echo {
		return q.TouchConversationOutbound(ctx, dbq.TouchConversationOutboundParams{ID: conv.ID, At: at, Preview: preview(h)})
	}
	return q.TouchConversationInbound(ctx, dbq.TouchConversationInboundParams{ID: conv.ID, At: at, Preview: preview(h)})
}

// applyKeyword honours STOP and START replies: the customer opts out of (or back in to)
// messages from this business, with the message as the consent record's evidence.
func applyKeyword(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, c dbq.Contact, body string, at time.Time) error {
	var st dbq.OptInStatus
	switch strings.ToUpper(strings.Trim(strings.TrimSpace(body), ".!")) {
	case "STOP", "STOP ALL", "UNSUBSCRIBE", "OPT OUT", "OPTOUT":
		st = dbq.OptInStatusOptedOut
	case "START", "SUBSCRIBE", "OPT IN", "OPTIN":
		st = dbq.OptInStatusOptedIn
	default:
		return nil
	}
	if c.OptInStatus == st {
		return nil
	}
	_, err := contacts.RecordConsent(ctx, q, tenantID, c.ID, st, dbq.ConsentSourceKeyword, body, nil, at)
	return err
}

// queueMediaDownload enqueues the copy of a message's file from Meta, in the transaction that
// stores the message.
func (p *Processor) queueMediaDownload(ctx context.Context, tx pgx.Tx, tenantID, messageID uuid.UUID, h MessageHeader) error {
	ref := map[string]*MediaRef{"image": h.Image, "video": h.Video, "audio": h.Audio, "document": h.Document, "sticker": h.Sticker}[h.Type]
	if ref == nil || ref.ID == "" {
		return nil
	}
	ins, err := jobs.From(ctx, p.Jobs)
	if err != nil {
		return err
	}
	_, err = ins.InsertTx(ctx, tx, media.DownloadArgs{
		TenantID: tenantID, MessageID: messageID, MetaMediaID: ref.ID, Filename: ref.Filename,
	}, nil)
	return err
}

// applyStatus records a delivery status for one of our outbound messages and moves the
// message forward if the status ranks higher than its current one.
func (p *Processor) applyStatus(ctx context.Context, q *dbq.Queries, tx pgx.Tx, tenantID uuid.UUID, phone dbq.PhoneNumber, s Status) error {
	st := dbq.MessageStatus(s.Status)
	switch st {
	case dbq.MessageStatusSent, dbq.MessageStatusDelivered, dbq.MessageStatusRead, dbq.MessageStatusFailed:
	default:
		return nil
	}
	msg, err := q.GetMessageByWamid(ctx, &s.ID)
	if db.IsNotFound(err) {
		// Meta can report a status before the send worker has stored the wamid. While a
		// message to this customer is still waiting for its wamid, retry the event shortly.
		at := unixTime(s.Timestamp, time.Now().UTC())
		if time.Since(at) < statusWaitLimit {
			pending, err := q.HasUnsentMessageTo(ctx, dbq.HasUnsentMessageToParams{PhoneNumberID: phone.ID, WaID: s.RecipientID})
			if err != nil {
				return err
			}
			if pending {
				return errStatusAhead
			}
		}
		return nil // not sent through Ecogo (for example from the Business app before echoes)
	}
	if err != nil {
		return err
	}
	var code *int32
	var title *string
	if len(s.Errors) > 0 {
		code, title = &s.Errors[0].Code, nonEmpty(s.Errors[0].Title)
	}
	params := dbq.AdvanceMessageStatusParams{ID: msg.ID, Status: st, ErrorCode: code, ErrorTitle: title}
	if s.Pricing != nil {
		params.PricingCategory, params.PricingBillable = nonEmpty(s.Pricing.Category), s.Pricing.Billable
	}
	moved, err := q.AdvanceMessageStatus(ctx, params)
	if err != nil {
		return err
	}
	rawStatus, _ := json.Marshal(s)
	err = q.InsertMessageStatusEvent(ctx, dbq.InsertMessageStatusEventParams{
		TenantID: tenantID, MessageID: msg.ID, Status: st, ErrorCode: code, ErrorTitle: title,
		OccurredAt: unixTime(s.Timestamp, time.Now().UTC()), Raw: rawStatus,
	})
	if err != nil || moved == 0 {
		return err
	}
	return messaging.EmitEvent(ctx, q, tx, p.Jobs, tenantID, msg.ID, webhooks.MessageStatus)
}

// statusWaitLimit bounds how long a status for an unknown wamid is retried.
const statusWaitLimit = 10 * time.Minute

var errStatusAhead = errors.New("status arrived before the message's wamid was stored")

func (p *Processor) templateStatus(ctx context.Context, entry Entry, raw json.RawMessage) error {
	var v TemplateStatusValue
	if err := json.Unmarshal(raw, &v); err != nil || v.MessageTemplateID == "" {
		return nil
	}
	st, ok := templates.StatusFromMeta(v.Event)
	if !ok {
		st = dbq.TemplateStatusDeleted // PENDING_DELETION and DELETED
		if e := strings.ToUpper(v.Event); e != "PENDING_DELETION" && e != "DELETED" {
			return nil
		}
	}
	var reason *string
	if v.Reason != nil && *v.Reason != "" && *v.Reason != "NONE" {
		reason = v.Reason
	}
	id := v.MessageTemplateID.String()
	return p.inTenant(ctx, "", entry.ID, func(q *dbq.Queries, tx pgx.Tx, tenantID uuid.UUID) error {
		// A template created outside Ecogo is not in our table yet; a template sync picks it up.
		rows, err := q.UpdateTemplateStatusByMetaID(ctx, dbq.UpdateTemplateStatusByMetaIDParams{Status: st, RejectedReason: reason, MetaTemplateID: &id})
		for _, t := range rows {
			if err == nil {
				err = webhooks.Emit(ctx, q, tx, p.Jobs, tenantID, webhooks.TemplateStatus, nil, templates.View(t))
			}
		}
		return err
	})
}

func (p *Processor) quality(ctx context.Context, entry Entry, raw json.RawMessage) error {
	var v QualityValue
	if err := json.Unmarshal(raw, &v); err != nil || v.CurrentLimit == "" {
		return nil
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, v.DisplayPhoneNumber)
	return p.inTenant(ctx, "", entry.ID, func(q *dbq.Queries, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := q.UpdatePhoneLimitTierByDisplay(ctx, dbq.UpdatePhoneLimitTierByDisplayParams{
			Tier: &v.CurrentLimit, WabaID: entry.ID, DisplayDigits: digits,
		})
		for _, ph := range rows {
			if err == nil {
				err = webhooks.Emit(ctx, q, tx, p.Jobs, tenantID, webhooks.NumberQuality, &ph.ID, numbers.View(ph, entry.ID))
			}
		}
		return err
	})
}

// accountUpdate handles the client removing Ecogo's access to their WABA: the token stops
// working, so it is deactivated and the account and its numbers show as revoked.
func (p *Processor) accountUpdate(ctx context.Context, entry Entry, raw json.RawMessage) error {
	var v AccountUpdateValue
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	if v.Event != "PARTNER_REMOVED" {
		p.log.Info("meta event: account update", "event", v.Event, "waba_id", entry.ID)
		return nil
	}
	return p.inTenant(ctx, "", entry.ID, func(q *dbq.Queries, _ pgx.Tx, _ uuid.UUID) error {
		acct, err := q.GetWhatsAppAccountByWabaID(ctx, entry.ID)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.RevokeWhatsAppAccount(ctx, acct.ID); err != nil {
			return err
		}
		if err := q.DeactivateCredentials(ctx, acct.ID); err != nil {
			return err
		}
		return q.SetAccountPhoneNumbersStatus(ctx, dbq.SetAccountPhoneNumbersStatusParams{
			WhatsappAccountID: acct.ID, Status: dbq.ConnectionStatusRevoked,
		})
	})
}

// stateSync imports contacts from a coexistence number's WhatsApp Business app. Removals are
// not applied: the contact may still have history here, and the client can delete it in Ecogo.
func (p *Processor) stateSync(ctx context.Context, entry Entry, raw json.RawMessage) error {
	var v StateSyncValue
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return p.inTenant(ctx, v.Metadata.PhoneNumberID, entry.ID, func(q *dbq.Queries, tx pgx.Tx, tenantID uuid.UUID) error {
		for _, s := range v.StateSync {
			if s.Type != "contact" || (s.Action != "add" && s.Action != "edit") {
				continue
			}
			num := strings.TrimPrefix(strings.ReplaceAll(s.Contact.PhoneNumber, " ", ""), "+")
			if !waID.MatchString(num) {
				continue
			}
			name := s.Contact.FullName
			if name == "" {
				name = s.Contact.FirstName
			}
			if _, err := q.UpsertContactFromWhatsApp(ctx, dbq.UpsertContactFromWhatsAppParams{
				ID: db.NewID(), TenantID: tenantID, WaID: num, Name: nonEmpty(name),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// preview is the short text shown in the inbox list.
func preview(h MessageHeader) string {
	var s string
	switch {
	case h.Text != nil:
		s = h.Text.Body
	case h.Image != nil && h.Image.Caption != "":
		s = h.Image.Caption
	case h.Video != nil && h.Video.Caption != "":
		s = h.Video.Caption
	case h.Document != nil && h.Document.Caption != "":
		s = h.Document.Caption
	case h.Button != nil:
		s = h.Button.Text
	case h.Reaction != nil:
		s = h.Reaction.Emoji
	case h.Interactive != nil && h.Interactive.ButtonReply != nil:
		s = h.Interactive.ButtonReply.Title
	case h.Interactive != nil && h.Interactive.ListReply != nil:
		s = h.Interactive.ListReply.Title
	default:
		s = "[" + h.Type + "]"
	}
	if utf8.RuneCountInString(s) > 120 {
		s = string([]rune(s)[:120]) + "…"
	}
	return s
}

// storedPayload keeps the delivery as jsonb; invalid JSON (never past the ingest check) is wrapped.
func storedPayload(b []byte) []byte {
	if json.Valid(b) {
		return b
	}
	out, _ := json.Marshal(map[string]string{"invalid": string(b)})
	return out
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
