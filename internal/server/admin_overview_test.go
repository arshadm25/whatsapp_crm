package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/admin"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

func TestAdminOverview(t *testing.T) {
	h := newHarness(t)
	_, shopMe, _ := h.connected()
	staff := h.platformAdmin("ops@ecogo.co.in")

	// Two errors now, one the day before, one too old to count.
	err := h.db.Global(context.Background(), func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO meta_api_errors (tenant_id, method, path, http_status, code, message, occurred_at) VALUES
			($1, 'POST', '/messages', 400, 131047, 'Re-engagement message', now() - interval '1 hour'),
			($1, 'POST', '/messages', 400, 131047, 'Re-engagement message', now() - interval '2 hours'),
			($1, 'POST', '/messages', 429, 130429, 'Rate limit hit', now() - interval '30 hours'),
			(NULL, 'GET', '/me', 500, 1, 'Unknown', now() - interval '60 hours')`, shopMe.Tenant.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var ov admin.Overview
	staff.do("GET", "/internal/admin/overview", nil, http.StatusOK, &ov)
	if ov.Tenants.Active != 2 || ov.Tenants.NewThisWeek != 2 || ov.Tenants.Suspended != 0 ||
		ov.MetaErrors.Hours != 24 || ov.MetaErrors.Current != 2 || ov.MetaErrors.Previous != 1 {
		t.Fatalf("overview = %+v", ov)
	}

	var groups struct{ Data []admin.MetaErrorGroup }
	staff.do("GET", "/internal/admin/meta-errors/summary?hours=48", nil, http.StatusOK, &groups)
	if len(groups.Data) != 2 || groups.Data[0].Count != 2 || *groups.Data[0].Code != 131047 || groups.Data[0].Tenants != 1 {
		t.Fatalf("groups = %+v", groups.Data)
	}
	var errs struct{ Data []admin.MetaError }
	staff.do("GET", "/internal/admin/meta-errors?hours=24&code=131047", nil, http.StatusOK, &errs)
	if len(errs.Data) != 2 {
		t.Fatalf("filtered errors = %+v", errs.Data)
	}
	staff.do("GET", "/internal/admin/meta-errors?hours=0", nil, http.StatusBadRequest, nil)

	// The tenant list carries its columns.
	var list struct{ Data []admin.ListedTenant }
	staff.do("GET", "/internal/admin/tenants?q=sharma", nil, http.StatusOK, &list)
	got := list.Data[0]
	if len(list.Data) != 1 || got.NumberCount != 1 || got.WabaID == nil || got.WabaCount != 1 || got.SubscriptionStatus == nil || *got.SubscriptionStatus != "trialing" {
		t.Fatalf("listed = %+v", got)
	}

	// Audit log export.
	status, body := staff.page("/internal/admin/audit-log.csv?tenant_id=" + shopMe.Tenant.ID.String())
	if status != http.StatusOK || !strings.HasPrefix(body, "Time (UTC),Workspace ID") || !strings.Contains(body, "tenant.create") {
		t.Fatalf("csv = %d %q", status, body)
	}
	var audit struct{ Data []admin.AuditEntry }
	staff.do("GET", "/internal/admin/audit-log?actor_type=platform_admin", nil, http.StatusOK, &audit)
	if len(audit.Data) == 0 || audit.Data[0].Action != "audit_log.export" {
		t.Fatalf("export not recorded: %+v", audit.Data)
	}
}
