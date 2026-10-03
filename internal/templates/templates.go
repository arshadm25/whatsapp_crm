// Package templates serves D4 message templates: listing, creating and editing them through
// Meta's review, deleting them, and syncing templates made elsewhere (WhatsApp Manager).
//
// Meta is the source of truth for a template's status. Create and edit call Meta in the request
// so the client sees Meta's validation errors right away; later status changes arrive as
// message_template_status_update webhooks (internal/metaevents).
package templates

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

// Meta is the part of the Graph client templates need.
type Meta interface {
	ListTemplates(ctx context.Context, token, wabaID string) ([]metaclient.Template, error)
	CreateTemplate(ctx context.Context, token, wabaID string, body map[string]any) (*metaclient.CreatedTemplate, error)
	EditTemplate(ctx context.Context, token, templateID string, body map[string]any) error
	DeleteTemplate(ctx context.Context, token, wabaID, name string) error
	ResumableUpload(ctx context.Context, token, mimeType, filename string, data []byte) (string, error)
}

// metaStatuses maps Meta's template statuses and review events to ours. Statuses missing here
// (PENDING_DELETION, DELETED, ARCHIVED) mean the template is gone.
var metaStatuses = map[string]dbq.TemplateStatus{
	"APPROVED":       dbq.TemplateStatusApproved,
	"PENDING":        dbq.TemplateStatusPending,
	"IN_REVIEW":      dbq.TemplateStatusPending,
	"REJECTED":       dbq.TemplateStatusRejected,
	"PAUSED":         dbq.TemplateStatusPaused,
	"DISABLED":       dbq.TemplateStatusDisabled,
	"LIMIT_EXCEEDED": dbq.TemplateStatusDisabled,
	"IN_APPEAL":      dbq.TemplateStatusInAppeal,
	"REINSTATED":     dbq.TemplateStatusApproved,
	"FLAGGED":        dbq.TemplateStatusApproved, // still sendable; quality drops are shown later in D4
}

// StatusFromMeta maps a Meta template status or review event; ok is false for a deleted template
// or a value we do not know.
func StatusFromMeta(s string) (st dbq.TemplateStatus, ok bool) {
	st, ok = metaStatuses[strings.ToUpper(s)]
	return st, ok
}

func categoryFromMeta(s string) (dbq.TemplateCategory, bool) {
	c := dbq.TemplateCategory(strings.ToLower(s))
	return c, c.Valid()
}

// rejectedReason drops Meta's "NONE" placeholder.
func rejectedReason(s string) *string {
	if s == "" || strings.EqualFold(s, "NONE") {
		return nil
	}
	return &s
}

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// Placeholders returns the distinct variables in a template text, in order of first use.
func Placeholders(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// Shape is what a send must supply for a template: variable counts for the header and body,
// and the header's media format, if any.
type Shape struct {
	HeaderFormat string // TEXT, IMAGE, VIDEO, DOCUMENT, LOCATION or ""
	HeaderVars   int
	BodyVars     int
}

// ShapeOf reads a stored template's components (Meta's shape).
func ShapeOf(components []byte) Shape {
	var comps []struct {
		Type   string `json:"type"`
		Format string `json:"format"`
		Text   string `json:"text"`
	}
	_ = json.Unmarshal(components, &comps)
	var s Shape
	for _, c := range comps {
		switch strings.ToUpper(c.Type) {
		case "HEADER":
			s.HeaderFormat = strings.ToUpper(c.Format)
			if s.HeaderFormat == "" {
				s.HeaderFormat = "TEXT"
			}
			s.HeaderVars = len(Placeholders(c.Text))
		case "BODY":
			s.BodyVars = len(Placeholders(c.Text))
		}
	}
	return s
}

// Syncer copies a WhatsApp account's templates from Meta into our table.
type Syncer struct {
	db   *db.DB
	meta Meta
}

func NewSyncer(d *db.DB, meta Meta) *Syncer { return &Syncer{db: d, meta: meta} }

// SyncAccount makes our templates for acct match Meta's: new and changed templates are
// upserted, and templates deleted at Meta are removed. It returns how many templates Meta has.
func (s *Syncer) SyncAccount(ctx context.Context, tenantID uuid.UUID, acct dbq.WhatsappAccount, token string) (int, error) {
	list, err := s.meta.ListTemplates(metaclient.WithTenant(ctx, tenantID.String()), token, acct.WabaID)
	if err != nil {
		return 0, err
	}
	n := 0
	err = s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		keep := []string{}
		for _, t := range list {
			st, ok := StatusFromMeta(t.Status)
			cat, catOK := categoryFromMeta(t.Category)
			if !ok || !catOK || t.ID == "" || t.Name == "" {
				slog.WarnContext(ctx, "templates: skipped template from Meta", "waba_id", acct.WabaID,
					"name", t.Name, "status", t.Status, "category", t.Category)
				continue
			}
			if _, err := q.UpsertTemplateFromMeta(ctx, fromMeta(tenantID, acct.ID, t, st, cat)); err != nil {
				return err
			}
			keep = append(keep, t.ID)
			n++
		}
		slog.InfoContext(ctx, "templates: synced from Meta", "waba_id", acct.WabaID, "from_meta", len(list), "kept", n)
		_, err := q.DeleteTemplatesMissingFromMeta(ctx, dbq.DeleteTemplatesMissingFromMetaParams{WhatsappAccountID: acct.ID, Keep: keep})
		return err
	})
	return n, err
}

func fromMeta(tenantID, accountID uuid.UUID, t metaclient.Template, st dbq.TemplateStatus, cat dbq.TemplateCategory) dbq.UpsertTemplateFromMetaParams {
	format := strings.ToLower(t.ParameterFormat)
	if format != "named" {
		format = "positional"
	}
	comps := []byte(t.Components)
	if len(comps) == 0 || string(comps) == "null" {
		comps = []byte("[]")
	}
	var quality *string
	if t.QualityScore != nil && t.QualityScore.Score != "" {
		quality = &t.QualityScore.Score
	}
	id := t.ID
	return dbq.UpsertTemplateFromMetaParams{
		ID: db.NewID(), TenantID: tenantID, WhatsappAccountID: accountID, MetaTemplateID: &id,
		Name: t.Name, Language: t.Language, Category: cat, Status: st,
		RejectedReason: rejectedReason(t.RejectedReason), QualityScore: quality,
		ParameterFormat: format, Components: comps,
	}
}
