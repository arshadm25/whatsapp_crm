package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/metafees"
)

type settingInt struct {
	Value, Deployment int
	Overridden        bool
}

type platformSettings struct {
	GSTRateBP    settingInt `json:"gst_rate_bp"`
	MetaMarkupBP settingInt `json:"meta_markup_bp"`
	AIModel      struct {
		Value, Deployment string
		Overridden        bool
	} `json:"ai_model"`
}

// Staff override the GST rate, the markup on Meta's fees and the AI model in Admin > Settings, and
// give one workspace its own markup. Statements use the workspace's markup, then the platform's,
// then the deployment's; null puts a value back on the deployment's.
func TestPlatformSettings(t *testing.T) {
	h := newHarness(t)
	owner, me, phone := h.connected()
	tenant := me.Tenant.ID
	staff := h.platformAdmin("ops@ecogo.co.in")

	var ps platformSettings
	staff.do("GET", "/internal/admin/settings", nil, http.StatusOK, &ps)
	if ps.GSTRateBP.Value != 1800 || ps.GSTRateBP.Overridden || ps.MetaMarkupBP.Overridden || ps.AIModel.Overridden {
		t.Fatalf("defaults = %+v", ps)
	}
	owner.do("GET", "/internal/admin/settings", nil, http.StatusNotFound, nil)
	owner.do("PUT", "/internal/admin/settings", map[string]any{"gst_rate_bp": 0, "reason": "Owner trying"}, http.StatusNotFound, nil)
	for _, bad := range []map[string]any{
		{"gst_rate_bp": 10001, "reason": "Rate change from accountant"},
		{"meta_markup_bp": -1, "reason": "Rate change from accountant"},
		{"ai_model": "two words", "reason": "Rate change from accountant"},
		{"gst_rate_bp": 1200, "reason": "no"},
	} {
		staff.do("PUT", "/internal/admin/settings", bad, http.StatusBadRequest, nil)
	}
	staff.do("PUT", "/internal/admin/settings", map[string]any{"gst_rate_bp": 1200, "meta_markup_bp": 1000, "ai_model": " claude-test-model ",
		"reason": "Accountant confirmed, ticket 42"}, http.StatusOK, &ps)
	if ps.GSTRateBP.Value != 1200 || ps.GSTRateBP.Deployment != 1800 || !ps.GSTRateBP.Overridden ||
		ps.MetaMarkupBP.Value != 1000 || ps.AIModel.Value != "claude-test-model" {
		t.Fatalf("saved = %+v", ps)
	}
	// Saving the same values again adds no audit entries.
	staff.do("PUT", "/internal/admin/settings", map[string]any{"gst_rate_bp": 1200, "meta_markup_bp": 1000, "ai_model": "claude-test-model",
		"reason": "Accountant confirmed, ticket 42"}, http.StatusOK, nil)
	var audited int
	h.scalar(tenant, "SELECT count(*) FROM audit_log WHERE action LIKE 'settings.%'", &audited)
	if audited != 3 {
		t.Fatalf("settings audit entries = %d", audited)
	}

	// The workspace pays Meta through Ecogo from August.
	staff.do("POST", "/internal/admin/tenants/"+tenant.String()+"/meta-payment-mode", map[string]any{"mode": "through_us", "reason": "Pays Meta through us"}, http.StatusOK, nil)
	err := h.db.Global(context.Background(), func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE tenants SET meta_payment_mode_since = '2026-08-01T00:00:00+05:30' WHERE id = $1", tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h.usage(tenant, phone.ID, "2026-08-10", "marketing", "IN", 100)
	h.usage(tenant, phone.ID, "2026-09-10", "marketing", "IN", 100)

	// August: the platform markup and GST rate.
	h.runStatements(time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))
	var fees metafees.Overview
	owner.do("GET", "/internal/billing/meta-fees", nil, http.StatusOK, &fees)
	want := metafees.Price(7846, 1000, 1200, true)
	if len(fees.Statements) != 1 || fees.Statements[0].MarkupMinor != want.MarkupMinor || fees.Statements[0].TotalMinor != want.TotalMinor {
		t.Fatalf("august = %+v, want %+v", fees.Statements, want)
	}

	// September: the workspace's own markup.
	path := "/internal/admin/tenants/" + tenant.String() + "/meta-markup"
	owner.do("POST", path, map[string]any{"markup_bp": 0, "reason": "Owner trying"}, http.StatusNotFound, nil)
	staff.do("POST", path, map[string]any{"markup_bp": 10001, "reason": "Negotiated rate"}, http.StatusBadRequest, nil)
	staff.do("POST", path, map[string]any{"markup_bp": 200, "reason": "no"}, http.StatusBadRequest, nil)
	var tv struct {
		MetaMarkupBP *int `json:"meta_markup_bp"`
	}
	staff.do("POST", path, map[string]any{"markup_bp": 200, "reason": "Negotiated rate, contract 7"}, http.StatusOK, &tv)
	if tv.MetaMarkupBP == nil || *tv.MetaMarkupBP != 200 {
		t.Fatalf("workspace markup = %+v", tv)
	}
	h.runStatements(time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC))
	owner.do("GET", "/internal/billing/meta-fees", nil, http.StatusOK, &fees)
	want = metafees.Price(7846, 200, 1200, true)
	if len(fees.Statements) != 2 || fees.Statements[0].Month != "2026-09" || fees.Statements[0].MarkupMinor != want.MarkupMinor || fees.Statements[0].TotalMinor != want.TotalMinor {
		t.Fatalf("september = %+v, want %+v", fees.Statements, want)
	}
	staff.do("GET", "/internal/admin/tenants/"+tenant.String(), nil, http.StatusOK, &tv)
	if tv.MetaMarkupBP == nil || *tv.MetaMarkupBP != 200 {
		t.Fatalf("detail markup = %+v", tv)
	}
	staff.do("POST", path, map[string]any{"markup_bp": nil, "reason": "Back to the standard rate"}, http.StatusOK, &tv)
	if tv.MetaMarkupBP != nil {
		t.Fatalf("cleared markup = %+v", tv)
	}
	h.scalar(tenant, fmt.Sprintf("SELECT count(*) FROM audit_log WHERE action = 'tenant.meta_markup' AND tenant_id = '%s'", tenant), &audited)
	if audited != 2 {
		t.Fatalf("workspace markup audit entries = %d", audited)
	}

	// Null goes back to the deployment's values.
	staff.do("PUT", "/internal/admin/settings", map[string]any{"gst_rate_bp": nil, "meta_markup_bp": nil, "ai_model": "",
		"reason": "Back to the deployment values"}, http.StatusOK, &ps)
	if ps.GSTRateBP.Value != 1800 || ps.GSTRateBP.Overridden || ps.MetaMarkupBP.Overridden || ps.AIModel.Overridden {
		t.Fatalf("reset = %+v", ps)
	}
	var seller struct {
		GSTRateBP int `json:"gst_rate_bp"`
	}
	staff.do("GET", "/internal/admin/seller", nil, http.StatusOK, &seller)
	if seller.GSTRateBP != 1800 {
		t.Fatalf("seller gst = %d", seller.GSTRateBP)
	}
}
