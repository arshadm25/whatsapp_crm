// Package contacts holds the contact representation shared by the API (D6).
package contacts

import (
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
