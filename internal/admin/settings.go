package admin

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// Matches the platform_settings.ai_model check.
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,99}$`)

// Defaults are the deployment's values for the settings staff can override in Admin > Settings.
type Defaults struct {
	MetaMarkupBP int
	AIModel      string
	AIConfigured bool // the deployment has an AI provider key
}

// WithDefaults sets the deployment values shown next to, and used without, the saved settings.
func (s *Service) WithDefaults(d Defaults) *Service {
	s.defaults = d
	return s
}

// Setting is one overridable value: what applies now, the deployment's value, and whether staff
// saved their own.
type Setting[T any] struct {
	Value      T    `json:"value"`
	Deployment T    `json:"deployment"`
	Overridden bool `json:"overridden"`
}

// PlatformSettings is Admin > Settings.
type PlatformSettings struct {
	GSTRateBP    Setting[int]    `json:"gst_rate_bp"`
	MetaMarkupBP Setting[int]    `json:"meta_markup_bp"`
	AIModel      Setting[string] `json:"ai_model"`
	AIConfigured bool            `json:"ai_configured"`
	UpdatedAt    *time.Time      `json:"updated_at"`
}

func pick[T any](own *T, base T) Setting[T] {
	if own != nil {
		return Setting[T]{Value: *own, Deployment: base, Overridden: true}
	}
	return Setting[T]{Value: base, Deployment: base}
}

func i32(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func (s *Service) settingsView(ps *dbq.PlatformSetting) PlatformSettings {
	out := PlatformSettings{AIConfigured: s.defaults.AIConfigured}
	var gst, markup *int
	var model *string
	if ps != nil {
		gst, markup, model = i32(ps.GstRateBp), i32(ps.MetaMarkupBp), ps.AiModel
		out.UpdatedAt = &ps.UpdatedAt
	}
	out.GSTRateBP = pick(gst, s.seller.GSTRateBP)
	out.MetaMarkupBP = pick(markup, s.defaults.MetaMarkupBP)
	out.AIModel = pick(model, s.defaults.AIModel)
	return out
}

func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) error {
	var out PlatformSettings
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		ps, err := q.GetPlatformSettings(r.Context())
		switch {
		case db.IsNotFound(err):
			out = s.settingsView(nil)
		case err != nil:
			return err
		default:
			out = s.settingsView(&ps)
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func validBP(field string, v *int, what string) error {
	if v != nil && (*v < 0 || *v > 10000) {
		return httpx.BadRequest(field, fmt.Sprintf("The %s is in basis points from 0 to 10000 (1800 is 18%%).", what))
	}
	return nil
}

func bpText(v *int32) string {
	if v == nil {
		return "deployment"
	}
	return fmt.Sprintf("%d", *v)
}

func strText(v *string) string {
	if v == nil {
		return "deployment"
	}
	return *v
}

func sameI32(a, b *int32) bool  { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
func sameStr(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// saveSettings replaces the platform settings. A null value goes back to the deployment's. Each
// changed value gets its own audit entry with the reason. Invoices and statements already issued
// keep the rates they were issued with.
func (s *Service) saveSettings(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		GSTRateBP    *int    `json:"gst_rate_bp"`
		MetaMarkupBP *int    `json:"meta_markup_bp"`
		AIModel      *string `json:"ai_model"`
		Reason       string  `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	why := strings.TrimSpace(req.Reason)
	if req.AIModel != nil {
		m := strings.TrimSpace(*req.AIModel)
		req.AIModel = &m
		if m == "" {
			req.AIModel = nil
		}
	}
	if err := validBP("gst_rate_bp", req.GSTRateBP, "GST rate"); err != nil {
		return err
	}
	if err := validBP("meta_markup_bp", req.MetaMarkupBP, "markup"); err != nil {
		return err
	}
	switch {
	case req.AIModel != nil && !modelPattern.MatchString(*req.AIModel):
		return httpx.BadRequest("ai_model", "Enter the model ID exactly as the provider names it, such as claude-haiku-4-5-20251001.")
	case len(why) < 5 || len(why) > 500:
		return httpx.BadRequest("reason", "Give a reason of 5 to 500 characters; it is kept in the audit log.")
	}
	toI32 := func(v *int) *int32 {
		if v == nil {
			return nil
		}
		x := int32(*v)
		return &x
	}
	p, _ := auth.PrincipalFrom(r.Context())
	var out PlatformSettings
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var cur dbq.PlatformSetting
		old, err := q.GetPlatformSettings(r.Context())
		switch {
		case err == nil:
			cur = old
		case !db.IsNotFound(err):
			return err
		}
		row, err := q.UpsertPlatformSettings(r.Context(), dbq.UpsertPlatformSettingsParams{
			GstRateBp: toI32(req.GSTRateBP), MetaMarkupBp: toI32(req.MetaMarkupBP), AiModel: req.AIModel, UpdatedBy: &p.UserID})
		if err != nil {
			return err
		}
		changes := []struct {
			same          bool
			action, value string
		}{
			{sameI32(cur.GstRateBp, row.GstRateBp), "settings.gst_rate", bpText(row.GstRateBp)},
			{sameI32(cur.MetaMarkupBp, row.MetaMarkupBp), "settings.meta_markup", bpText(row.MetaMarkupBp)},
			{sameStr(cur.AiModel, row.AiModel), "settings.ai_model", strText(row.AiModel)},
		}
		for _, c := range changes {
			if c.same {
				continue
			}
			if err := s.record(r, q, nil, c.action, "setting", c.value, &why); err != nil {
				return err
			}
		}
		out = s.settingsView(&row)
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// setTenantMarkup gives one workspace its own markup on Meta's fees, or (null) puts it back on
// the platform's. It applies to statements issued from now on.
func (s *Service) setTenantMarkup(w http.ResponseWriter, r *http.Request) error {
	id, err := tenantID(r)
	if err != nil {
		return err
	}
	var req struct {
		MarkupBP *int   `json:"markup_bp"`
		Reason   string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	why := strings.TrimSpace(req.Reason)
	if err := validBP("markup_bp", req.MarkupBP, "markup"); err != nil {
		return err
	}
	if len(why) < 5 || len(why) > 500 {
		return httpx.BadRequest("reason", "Give a reason of 5 to 500 characters; it is kept in the audit log.")
	}
	var bp *int32
	if req.MarkupBP != nil {
		v := int32(*req.MarkupBP)
		bp = &v
	}
	var t dbq.Tenant
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		cur, err := q.GetTenant(r.Context(), id)
		if db.IsNotFound(err) {
			return httpx.NewError(http.StatusNotFound, "not_found", "Workspace not found.")
		}
		if err != nil {
			return err
		}
		if sameI32(cur.MetaMarkupBp, bp) {
			t = cur
			return nil
		}
		if t, err = q.SetTenantMetaMarkup(r.Context(), dbq.SetTenantMetaMarkupParams{ID: id, MarkupBp: bp}); err != nil {
			return err
		}
		return s.record(r, q, &id, "tenant.meta_markup", "tenant", id.String(), &why)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, tenantView(t))
	return nil
}
