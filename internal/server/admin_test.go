package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/admin"
	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

func TestTwoStepVerification(t *testing.T) {
	h := newHarness(t)
	c := h.newClient()
	c.signup("asha@example.com", "Asha Textiles")

	var setup auth.TOTPSetup
	c.do("POST", "/internal/auth/2fa/setup", nil, http.StatusOK, &setup)
	c.do("POST", "/internal/auth/2fa/enable", map[string]string{"secret": setup.Secret, "code": "000000"}, http.StatusBadRequest, nil)
	var me auth.MeResponse
	c.do("POST", "/internal/auth/2fa/enable", map[string]string{"secret": setup.Secret, "code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusOK, &me)
	if !me.User.TwoFactorEnabled {
		t.Fatalf("2fa not enabled: %+v", me.User)
	}
	// This session already proved the code.
	c.do("GET", "/internal/team/members", nil, http.StatusOK, nil)

	// A new login asks for the code before anything else.
	other := h.newClient()
	var pending map[string]any
	other.do("POST", "/internal/auth/login", map[string]string{"email": "asha@example.com", "password": "a long enough password"}, http.StatusOK, &pending)
	if pending["mfa_required"] != true {
		t.Fatalf("login = %v", pending)
	}
	other.do("GET", "/internal/auth/me", nil, http.StatusOK, &pending)
	if pending["mfa_required"] != true {
		t.Fatalf("me = %v", pending)
	}
	other.do("GET", "/internal/team/members", nil, http.StatusForbidden, nil)
	other.do("POST", "/internal/auth/2fa/verify", map[string]string{"code": "123456"}, http.StatusBadRequest, nil)
	other.do("POST", "/internal/auth/2fa/verify", map[string]string{"code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusOK, &me)
	if me.Tenant == nil {
		t.Fatalf("verified me = %+v", me)
	}
	other.do("GET", "/internal/team/members", nil, http.StatusOK, nil)

	// Wrong codes are limited per user.
	third := h.newClient()
	third.do("POST", "/internal/auth/login", map[string]string{"email": "asha@example.com", "password": "a long enough password"}, http.StatusOK, nil)
	for i := 0; i < 4; i++ {
		third.do("POST", "/internal/auth/2fa/verify", map[string]string{"code": "000000"}, http.StatusBadRequest, nil)
	}
	third.do("POST", "/internal/auth/2fa/verify", map[string]string{"code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusTooManyRequests, nil)

	// Turning it off needs a current code.
	c.do("POST", "/internal/auth/2fa/disable", map[string]string{"code": "000000"}, http.StatusBadRequest, nil)
	c.do("POST", "/internal/auth/2fa/disable", map[string]string{"code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusOK, &me)
	if me.User.TwoFactorEnabled {
		t.Fatal("2fa still on")
	}
}

func TestAdminConsole(t *testing.T) {
	h := newHarness(t)
	shop, shopMe, _ := h.connected()
	h.inbound("919812345678", "wamid.admin1", "Where is my order?")
	staff := h.newClient()
	staff.signup("ops@ecogo.co.in", "Ecogo")

	// Not an admin: the console does not exist.
	staff.do("GET", "/internal/admin/tenants", nil, http.StatusNotFound, nil)
	err := h.db.Global(context.Background(), func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.SetPlatformAdmin(context.Background(), dbq.SetPlatformAdminParams{Email: "ops@ecogo.co.in", IsPlatformAdmin: true})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Admins need two-step verification first, and cannot turn it off.
	staff.do("GET", "/internal/admin/tenants", nil, http.StatusForbidden, nil)
	var setup auth.TOTPSetup
	staff.do("POST", "/internal/auth/2fa/setup", nil, http.StatusOK, &setup)
	staff.do("POST", "/internal/auth/2fa/enable", map[string]string{"secret": setup.Secret, "code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusOK, nil)
	staff.do("POST", "/internal/auth/2fa/disable", map[string]string{"code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusConflict, nil)

	var list struct {
		Data       []admin.Tenant
		NextCursor *string `json:"next_cursor"`
	}
	staff.do("GET", "/internal/admin/tenants?limit=1", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.NextCursor == nil {
		t.Fatalf("page 1 = %+v", list)
	}
	staff.do("GET", "/internal/admin/tenants?limit=1&cursor="+*list.NextCursor, nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.NextCursor != nil {
		t.Fatalf("page 2 = %+v", list)
	}
	staff.do("GET", "/internal/admin/tenants?q=sharma", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.Data[0].ID != shopMe.Tenant.ID {
		t.Fatalf("search = %+v", list)
	}
	tenant := "/internal/admin/tenants/" + shopMe.Tenant.ID.String()
	var detail admin.TenantDetail
	staff.do("GET", tenant, nil, http.StatusOK, &detail)
	if detail.Members != 1 || detail.Status != "active" || detail.LastMessageAt == nil || len(detail.Numbers) != 1 {
		t.Fatalf("detail = %+v", detail)
	}

	// Suspending locks the workspace out until it is reactivated.
	staff.do("POST", tenant+"/suspend", map[string]string{"reason": ""}, http.StatusBadRequest, nil)
	staff.do("POST", tenant+"/suspend", map[string]string{"reason": "Spam complaints from Meta"}, http.StatusOK, &detail.Tenant)
	if detail.Status != "suspended" || detail.SuspendedReason == nil {
		t.Fatalf("suspended = %+v", detail.Tenant)
	}
	shop.do("GET", "/internal/team/members", nil, http.StatusForbidden, nil)
	staff.do("POST", tenant+"/reactivate", map[string]string{"reason": "Resolved with the customer"}, http.StatusOK, &detail.Tenant)
	shop.do("POST", "/internal/auth/switch-tenant", map[string]string{"tenant_id": shopMe.Tenant.ID.String()}, http.StatusOK, nil)
	shop.do("GET", "/internal/team/members", nil, http.StatusOK, nil)

	// A workspace owner cannot reach the console.
	shop.do("GET", "/internal/admin/audit-log", nil, http.StatusNotFound, nil)

	staff.do("GET", "/internal/admin/webhook-health?hours=999", nil, http.StatusBadRequest, nil)
	var health admin.WebhookHealth
	staff.do("GET", "/internal/admin/webhook-health", nil, http.StatusOK, &health)
	staff.do("GET", "/internal/admin/meta-errors", nil, http.StatusOK, nil)

	var convs struct{ Data []admin.Conversation }
	staff.do("GET", tenant+"/conversations", nil, http.StatusOK, &convs)
	if len(convs.Data) != 1 || convs.Data[0].ContactWaID != "919812345678" {
		t.Fatalf("conversations = %+v", convs)
	}
	read := tenant + "/conversations/" + convs.Data[0].ID.String() + "/messages"
	staff.do("POST", read, map[string]string{}, http.StatusBadRequest, nil)
	staff.do("POST", tenant+"/conversations/"+shopMe.Tenant.ID.String()+"/messages", map[string]string{"reason": "Support ticket 42"}, http.StatusNotFound, nil)
	var msgs struct{ Data []messaging.Message }
	staff.do("POST", read, map[string]string{"reason": "Support ticket 42"}, http.StatusOK, &msgs)
	if len(msgs.Data) != 1 || !strings.Contains(string(msgs.Data[0].Content), "Where is my order?") {
		t.Fatalf("messages = %+v", msgs)
	}

	var audit struct{ Data []admin.AuditEntry }
	staff.do("GET", "/internal/admin/audit-log?actor_type=platform_admin&tenant_id="+shopMe.Tenant.ID.String(), nil, http.StatusOK, &audit)
	if len(audit.Data) != 3 || audit.Data[0].Action != "message_content.view" || *audit.Data[0].Reason != "Support ticket 42" ||
		audit.Data[1].Action != "tenant.reactivate" || audit.Data[2].Action != "tenant.suspend" ||
		audit.Data[2].Reason == nil || *audit.Data[2].Reason != "Spam complaints from Meta" || audit.Data[0].ActorEmail == nil {
		t.Fatalf("audit = %+v", audit.Data)
	}
}
