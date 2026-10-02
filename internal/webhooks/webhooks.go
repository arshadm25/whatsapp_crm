// Package webhooks sends events to clients' own HTTPS endpoints (D8): message.received,
// message.status, template.status and number.quality. An event is stored as one delivery per
// subscribed endpoint in the transaction that caused it, and a River job posts each delivery,
// signed, retrying with backoff for about a day.
package webhooks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

// Event types.
const (
	MessageReceived = "message.received"
	MessageStatus   = "message.status"
	TemplateStatus  = "template.status"
	NumberQuality   = "number.quality"
	BotHandoff      = "bot.handoff"
)

// EventTypes lists every event type a client can subscribe to.
var EventTypes = []string{MessageReceived, MessageStatus, TemplateStatus, NumberQuality, BotHandoff}

// Envelope matches the EventEnvelope schema in api/openapi.yaml.
type Envelope struct {
	ID        uuid.UUID `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Data      any       `json:"data"`
}

// Emit records an event for every endpoint subscribed to it and queues the deliveries, in the
// caller's tenant transaction. phoneNumberID narrows it to endpoints for that number (or all).
func Emit(ctx context.Context, q *dbq.Queries, tx pgx.Tx, ins jobs.Inserter, tenantID uuid.UUID, typ string, phoneNumberID *uuid.UUID, data any) error {
	env := Envelope{ID: db.NewID(), Type: typ, CreatedAt: time.Now().UTC(), TenantID: tenantID, Data: data}
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	ids, err := q.InsertDeliveriesForEvent(ctx, dbq.InsertDeliveriesForEventParams{
		EventID: env.ID, EventType: typ, Payload: payload, PhoneNumberID: phoneNumberID,
	})
	if err != nil || len(ids) == 0 {
		return err
	}
	ins, err = jobs.From(ctx, ins)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := ins.InsertTx(ctx, tx, DeliverArgs{TenantID: tenantID, DeliveryID: id}, nil); err != nil {
			return err
		}
	}
	return nil
}
