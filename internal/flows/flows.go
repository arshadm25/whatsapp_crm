// Package flows manages WhatsApp Flows (BRD Phase 2): the forms customers fill in inside
// WhatsApp. A Flow is created, edited, previewed and published through Meta's Flows API, and each
// submission a customer sends is stored against their contact and sent to the client's webhooks
// as flow.submission.
package flows

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

// Categories are the purposes Meta accepts for a Flow.
var Categories = []string{
	"SIGN_UP", "SIGN_IN", "APPOINTMENT_BOOKING", "LEAD_GENERATION", "CONTACT_US", "CUSTOMER_SUPPORT", "SURVEY", "OTHER",
}

// StarterJSON is the Flow a new designer starts from: one screen that asks for a name and a
// phone number and submits them.
const StarterJSON = `{
  "version": "6.3",
  "screens": [
    {
      "id": "DETAILS",
      "title": "Your details",
      "terminal": true,
      "success": true,
      "data": {},
      "layout": {
        "type": "SingleColumnLayout",
        "children": [
          {"type": "TextInput", "name": "name", "label": "Your name", "input-type": "text", "required": true},
          {"type": "TextInput", "name": "phone", "label": "Phone number", "input-type": "phone", "required": true},
          {"type": "Footer", "label": "Submit", "on-click-action": {"name": "complete", "payload": {"name": "${form.name}", "phone": "${form.phone}"}}}
        ]
      }
    }
  ]
}`

// ValidFlowJSON makes the checks Meta's validator explains poorly: it must be a JSON object
// with a version and at least one screen. Meta validates the rest.
func ValidFlowJSON(raw []byte) error {
	var f struct {
		Version string            `json:"version"`
		Screens []json.RawMessage `json:"screens"`
	}
	if len(raw) > 5<<20 {
		return fmt.Errorf("flow_json is too large")
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("flow_json must be a JSON object: %v", err)
	}
	if f.Version == "" {
		return fmt.Errorf("flow_json needs a version, for example \"6.3\"")
	}
	if len(f.Screens) == 0 {
		return fmt.Errorf("flow_json needs at least one screen")
	}
	return nil
}

// MetaStatus maps Meta's Flow status to ours.
func MetaStatus(s string) (string, bool) {
	switch st := strings.ToLower(s); st {
	case "draft", "published", "deprecated", "blocked", "throttled":
		return st, true
	}
	return "", false
}

// Submission matches the FlowSubmission schema in api/openapi.yaml.
type Submission struct {
	ID             uuid.UUID       `json:"id"`
	FlowID         *uuid.UUID      `json:"flow_id"`
	FlowName       *string         `json:"flow_name"`
	MetaFlowID     *string         `json:"meta_flow_id"`
	ContactID      uuid.UUID       `json:"contact_id"`
	ContactWaID    string          `json:"contact_wa_id"`
	ContactName    *string         `json:"contact_name"`
	ConversationID uuid.UUID       `json:"conversation_id"`
	MessageID      uuid.UUID       `json:"message_id"`
	Response       json.RawMessage `json:"response"`
	CreatedAt      time.Time       `json:"created_at"`
}

func submissionView(s dbq.FlowSubmission, waID string, name, flowName *string) Submission {
	return Submission{
		ID: s.ID, FlowID: s.FlowID, FlowName: flowName, MetaFlowID: s.MetaFlowID, ContactID: s.ContactID, ContactWaID: waID,
		ContactName: name, ConversationID: s.ConversationID, MessageID: s.MessageID, Response: s.Response, CreatedAt: s.CreatedAt,
	}
}

// ParseResponse splits the response_json of a Flow reply into its flow_token and the answers.
func ParseResponse(responseJSON string) (token string, answers map[string]any, err error) {
	answers = map[string]any{}
	if err = json.Unmarshal([]byte(responseJSON), &answers); err != nil {
		return "", nil, err
	}
	if t, ok := answers["flow_token"].(string); ok {
		token = t
	}
	delete(answers, "flow_token")
	return token, answers, nil
}

// Flatten turns answers into text values, for message templates and bot variables. Lists (a
// checkbox group) join with commas; anything else non-text is written as JSON.
func Flatten(answers map[string]any) map[string]string {
	out := make(map[string]string, len(answers))
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := answers[k].(type) {
		case string:
			out[k] = v
		case []any:
			parts := make([]string, len(v))
			for i, x := range v {
				if s, ok := x.(string); ok {
					parts[i] = s
				} else {
					b, _ := json.Marshal(x)
					parts[i] = string(b)
				}
			}
			out[k] = strings.Join(parts, ", ")
		case nil:
			out[k] = ""
		default:
			b, _ := json.Marshal(v)
			out[k] = strings.Trim(string(b), `"`)
		}
	}
	return out
}

// RecordSubmission stores a customer's Flow reply against their contact and emits flow.submission.
// replyTo is the wamid of the Flow message the customer answered; it tells which Flow it was.
// Call it in the transaction that stores the inbound message.
func RecordSubmission(ctx context.Context, q *dbq.Queries, tx pgx.Tx, ins jobs.Inserter, tenantID uuid.UUID,
	contact dbq.Contact, conversationID, messageID uuid.UUID, replyTo, responseJSON string) error {
	token, answers, err := ParseResponse(responseJSON)
	if err != nil {
		return nil // not a Flow reply we understand; the message itself is stored
	}
	raw, err := json.Marshal(answers)
	if err != nil {
		return err
	}
	var (
		flowID     *uuid.UUID
		flowName   *string
		metaFlowID *string
	)
	if replyTo != "" {
		if m, err := q.GetMessageByWamid(ctx, &replyTo); err == nil {
			var c struct {
				Interactive struct {
					Action struct {
						Parameters struct {
							FlowID string `json:"flow_id"`
						} `json:"parameters"`
					} `json:"action"`
				} `json:"interactive"`
			}
			if json.Unmarshal(m.Content, &c) == nil && c.Interactive.Action.Parameters.FlowID != "" {
				id := c.Interactive.Action.Parameters.FlowID
				metaFlowID = &id
				if f, err := q.GetFlowByMetaID(ctx, id); err == nil {
					flowID, flowName = &f.ID, &f.Name
				} else if !db.IsNotFound(err) {
					return err
				}
			}
		} else if !db.IsNotFound(err) {
			return err
		}
	}
	var tok *string
	if token != "" {
		tok = &token
	}
	row, err := q.InsertFlowSubmission(ctx, dbq.InsertFlowSubmissionParams{
		ID: db.NewID(), TenantID: tenantID, FlowID: flowID, MetaFlowID: metaFlowID, ContactID: contact.ID,
		ConversationID: conversationID, MessageID: messageID, FlowToken: tok, Response: raw,
	})
	if db.IsNotFound(err) {
		return nil // Meta re-sent the message
	}
	if err != nil {
		return err
	}
	name := contact.Name
	if name == nil {
		name = contact.ProfileName
	}
	conv, err := q.GetConversationView(ctx, conversationID)
	if err != nil {
		return err
	}
	return webhooks.Emit(ctx, q, tx, ins, tenantID, webhooks.FlowSubmission, &conv.Conversation.PhoneNumberID,
		submissionView(row, contact.WaID, name, flowName))
}

// ApplyStatusEvent records a status change Meta reports for a Flow (the "flows" webhook field).
func ApplyStatusEvent(ctx context.Context, q *dbq.Queries, metaFlowID, newStatus string) error {
	st, ok := MetaStatus(newStatus)
	if !ok {
		return nil
	}
	f, err := q.GetFlowByMetaID(ctx, metaFlowID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if f.Status == st {
		return nil
	}
	_, err = q.UpdateFlowStatus(ctx, dbq.UpdateFlowStatusParams{ID: f.ID, Status: st})
	return err
}
