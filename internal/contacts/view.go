// Package contacts holds the contact representation shared by the API (D6).
package contacts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// Contact matches the Contact schema in api/openapi.yaml.
type Contact struct {
	ID           uuid.UUID       `json:"id"`
	WaID         string          `json:"wa_id"`
	Name         *string         `json:"name"`
	ProfileName  *string         `json:"profile_name"`
	Language     *string         `json:"language"`
	CustomFields json.RawMessage `json:"custom_fields"`
	Tags         []string        `json:"tags"`
	OptInStatus  string          `json:"opt_in_status"`
	OptedInAt    *time.Time      `json:"opted_in_at"`
	OptedOutAt   *time.Time      `json:"opted_out_at"`
	Blocked      bool            `json:"blocked"`
	CreatedAt    time.Time       `json:"created_at"`
}

func View(c dbq.Contact, tags []string) Contact {
	if tags == nil {
		tags = []string{}
	}
	fields := json.RawMessage(c.CustomFields)
	if len(fields) == 0 {
		fields = json.RawMessage("{}")
	}
	return Contact{
		ID: c.ID, WaID: c.WaID, Name: c.Name, ProfileName: c.ProfileName, Language: c.Language,
		CustomFields: fields, Tags: tags, OptInStatus: string(c.OptInStatus), OptedInAt: c.OptedInAt,
		OptedOutAt: c.OptedOutAt, Blocked: c.Blocked, CreatedAt: c.CreatedAt,
	}
}

// Tags groups tag rows by contact.
func Tags(rows []dbq.ListContactTagsRow) map[uuid.UUID][]string {
	out := map[uuid.UUID][]string{}
	for _, r := range rows {
		out[r.ContactID] = append(out[r.ContactID], r.Name)
	}
	return out
}

// Listed is a contact as the list and GET return it: with when they last messaged, where their
// latest opt-in came from and, on GET only, how many conversations they have had.
type Listed struct {
	Contact
	LastMessageAt     *time.Time `json:"last_message_at"`
	OptInSource       *string    `json:"opt_in_source"`
	ConversationCount *int32     `json:"conversation_count,omitempty"`
}

// activity renders contacts with their last message time and opt-in source.
func activity(ctx context.Context, q *dbq.Queries, cs []dbq.Contact) ([]Listed, error) {
	ids := make([]uuid.UUID, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	tagRows, err := q.ListContactTags(ctx, ids)
	if err != nil {
		return nil, err
	}
	last, err := q.ContactLastMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	sources, err := q.ContactOptInSources(ctx, ids)
	if err != nil {
		return nil, err
	}
	lastBy := map[uuid.UUID]time.Time{}
	for _, l := range last {
		lastBy[l.ContactID] = l.LastMessageAt
	}
	sourceBy := map[uuid.UUID]string{}
	for _, s := range sources {
		sourceBy[s.ContactID] = string(s.Source)
	}
	tags := Tags(tagRows)
	out := make([]Listed, 0, len(cs))
	for _, c := range cs {
		l := Listed{Contact: View(c, tags[c.ID])}
		if t, ok := lastBy[c.ID]; ok {
			l.LastMessageAt = &t
		}
		if s, ok := sourceBy[c.ID]; ok {
			l.OptInSource = &s
		}
		out = append(out, l)
	}
	return out, nil
}
