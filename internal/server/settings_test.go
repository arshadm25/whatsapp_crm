package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/inbox"
	"github.com/arshadm25/whatsapp_crm/internal/notifications"
)

type notificationList struct {
	Data   []notifications.Notification `json:"data"`
	Unread int                          `json:"unread"`
}

func TestNotifications(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	h.inbound("919800000001", "wamid.N1", "Hello")
	var page struct{ Data []inbox.Conversation }
	c.do("GET", "/v1/conversations", nil, http.StatusOK, &page)
	conv := page.Data[0].ID.String()

	// Assigning a conversation tells the assignee.
	c.do("PATCH", "/v1/conversations/"+conv, map[string]any{"assignee_id": me.User.ID}, http.StatusOK, nil)
	var list notificationList
	c.do("GET", "/internal/notifications", nil, http.StatusOK, &list)
	if list.Unread != 1 || len(list.Data) != 1 || list.Data[0].Kind != "conversation_assigned" ||
		list.Data[0].Link == nil || *list.Data[0].Link != "/inbox/"+conv || list.Data[0].Body != "Ravi" {
		t.Fatalf("notifications = %+v", list)
	}

	// Turned off, it no longer appears.
	var settings struct{ Data []notifications.Kind }
	c.do("GET", "/internal/notifications/settings", nil, http.StatusOK, &settings)
	if len(settings.Data) != len(notifications.Kinds) || !settings.Data[0].InApp || settings.Data[0].Email {
		t.Fatalf("default settings = %+v", settings.Data)
	}
	c.do("PUT", "/internal/notifications/settings", map[string]any{"data": []map[string]any{{"kind": "nope", "in_app": true, "email": true}}}, http.StatusBadRequest, nil)
	c.do("PUT", "/internal/notifications/settings", map[string]any{"data": []map[string]any{{"kind": "conversation_assigned", "in_app": false, "email": false}}}, http.StatusOK, &settings)
	if settings.Data[0].InApp || !settings.Data[1].InApp {
		t.Fatalf("saved settings = %+v", settings.Data)
	}
	c.do("PATCH", "/v1/conversations/"+conv, map[string]any{"assignee_id": nil}, http.StatusOK, nil)
	c.do("PATCH", "/v1/conversations/"+conv, map[string]any{"assignee_id": me.User.ID}, http.StatusOK, nil)
	c.do("GET", "/internal/notifications", nil, http.StatusOK, &list)
	if len(list.Data) != 1 {
		t.Fatalf("notified while turned off: %+v", list.Data)
	}

	c.do("POST", "/internal/notifications/read", nil, http.StatusNoContent, nil)
	c.do("GET", "/internal/notifications", nil, http.StatusOK, &list)
	if list.Unread != 0 || list.Data[0].ReadAt == nil {
		t.Fatalf("after read = %+v", list)
	}

	// A quality drop notifies owners and admins in the app and, by default, by email.
	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE phone_numbers SET quality_rating = 'green'")
		if err == nil {
			_, err = tx.Exec(context.Background(), "UPDATE phone_numbers SET quality_rating = 'yellow'")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/internal/notifications", nil, http.StatusOK, &list)
	if list.Unread != 1 || list.Data[0].Kind != "number_quality" || !strings.Contains(list.Data[0].Title, "dropped to yellow") {
		t.Fatalf("quality notification = %+v", list.Data)
	}
	w := notifications.NewEmailWorker(h.db, h.mail, "http://localhost", h.log)
	for range 2 {
		if err := w.Work(context.Background(), &river.Job[notifications.EmailArgs]{JobRow: &rivertype.JobRow{}}); err != nil {
			t.Fatal(err)
		}
	}
	var emails int
	for _, m := range h.mail.sent {
		if m.To == "owner@example.com" && strings.Contains(m.Subject, "dropped to yellow") {
			emails++
			if !strings.Contains(m.Text, "http://localhost/numbers") {
				t.Fatalf("email = %q", m.Text)
			}
		}
	}
	if emails != 1 {
		t.Fatalf("quality emails = %d", emails)
	}

	// Email only: nothing in the bell, but the email goes out.
	c.do("PUT", "/internal/notifications/settings", map[string]any{"data": []map[string]any{{"kind": "number_quality", "in_app": false, "email": true}}}, http.StatusOK, nil)
	err = h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE phone_numbers SET quality_rating = 'red'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/internal/notifications", nil, http.StatusOK, &list)
	if list.Unread != 1 || len(list.Data) != 2 {
		t.Fatalf("email-only notification shown: %+v", list)
	}
	if err := w.Work(context.Background(), &river.Job[notifications.EmailArgs]{JobRow: &rivertype.JobRow{}}); err != nil {
		t.Fatal(err)
	}
	if m, _ := h.mail.last("owner@example.com"); !strings.Contains(m.Subject, "dropped to red") {
		t.Fatalf("last email = %q", m.Subject)
	}
}

// rawLogin logs in and returns the session cookie the server set.
func rawLogin(t *testing.T, c *client, email, password string, remember bool) *http.Cookie {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"email": email, "password": password, "remember": remember})
	req, _ := http.NewRequest("POST", c.h.api.URL+"/internal/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	u, _ := url.Parse(c.h.api.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == auth.CSRFCookie {
			req.Header.Set(auth.CSRFHeader, ck.Value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.SessionCookie {
			return ck
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestKeepMeLoggedInAndPasswordReset(t *testing.T) {
	h := newHarness(t)
	c := h.newClient()
	c.signup("owner@example.com", "Sharma Sweets")

	if ck := rawLogin(t, h.newClient(), "owner@example.com", "a long enough password", false); ck.MaxAge != 0 {
		t.Fatalf("browser-session cookie has max-age %d", ck.MaxAge)
	}
	remembered := h.newClient()
	if ck := rawLogin(t, remembered, "owner@example.com", "a long enough password", true); ck.MaxAge != int(auth.RememberTTL.Seconds()) {
		t.Fatalf("remembered cookie max-age = %d", ck.MaxAge)
	}

	// Asking for a reset says the same for unknown emails, and sends nothing.
	before := len(h.mail.sent)
	anon := h.newClient()
	anon.do("POST", "/internal/auth/password/forgot", map[string]string{"email": "nobody@example.com"}, http.StatusNoContent, nil)
	if len(h.mail.sent) != before {
		t.Fatal("email sent for an unknown address")
	}
	anon.do("POST", "/internal/auth/password/forgot", map[string]string{"email": "Owner@Example.com"}, http.StatusNoContent, nil)
	m, ok := h.mail.last("owner@example.com")
	if !ok || !strings.Contains(m.Subject, "Reset") {
		t.Fatalf("reset email = %+v", m)
	}
	i := strings.Index(m.Text, "/reset-password?token=")
	token := strings.Fields(m.Text[i+len("/reset-password?token="):])[0]

	anon.do("POST", "/internal/auth/password/reset", map[string]string{"token": token, "password": "short"}, http.StatusBadRequest, nil)
	anon.do("POST", "/internal/auth/password/reset", map[string]string{"token": token + "x", "password": "a brand new password"}, http.StatusBadRequest, nil)
	anon.do("POST", "/internal/auth/password/reset", map[string]string{"token": token, "password": "a brand new password"}, http.StatusNoContent, nil)
	// The link works once, and every session is logged out.
	anon.do("POST", "/internal/auth/password/reset", map[string]string{"token": token, "password": "another new password"}, http.StatusBadRequest, nil)
	c.do("GET", "/internal/auth/me", nil, http.StatusUnauthorized, nil)
	remembered.do("GET", "/internal/auth/me", nil, http.StatusUnauthorized, nil)
	anon.do("POST", "/internal/auth/login", map[string]string{"email": "owner@example.com", "password": "a long enough password"}, http.StatusUnauthorized, nil)
	anon.do("POST", "/internal/auth/login", map[string]string{"email": "owner@example.com", "password": "a brand new password"}, http.StatusOK, nil)
}

func TestRequireTwoFactorAndResendInvite(t *testing.T) {
	h := newHarness(t)
	owner := h.newClient()
	owner.signup("owner@example.com", "Sharma Sweets")

	// The owner must have it on before requiring it.
	owner.do("PUT", "/internal/team/workspace/two-factor", map[string]bool{"required": true}, http.StatusConflict, nil)
	var setup auth.TOTPSetup
	owner.do("POST", "/internal/auth/2fa/setup", nil, http.StatusOK, &setup)
	owner.do("POST", "/internal/auth/2fa/enable", map[string]string{"secret": setup.Secret, "code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusOK, nil)
	var ws auth.Workspace
	owner.do("PUT", "/internal/team/workspace/two-factor", map[string]bool{"required": true}, http.StatusOK, &ws)
	if !ws.RequireTwoFactor {
		t.Fatalf("workspace = %+v", ws)
	}
	owner.do("POST", "/internal/auth/2fa/disable", map[string]string{"code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusConflict, nil)

	// Resending an invite replaces its link.
	var inv, resent auth.Invite
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "ravi@example.com", "role": "agent"}, http.StatusCreated, &inv)
	owner.do("POST", "/internal/team/invites/"+inv.ID.String()+"/resend", nil, http.StatusOK, &resent)
	if resent.Link == "" || resent.Link == inv.Link || resent.ID != inv.ID {
		t.Fatalf("resent = %+v", resent)
	}
	if m, _ := h.mail.last("ravi@example.com"); !strings.Contains(m.Text, resent.Link) {
		t.Fatalf("resent email = %q", m.Text)
	}
	ravi := h.newClient()
	ravi.do("GET", "/internal/auth/invites/"+inviteToken(t, inv.Link), nil, http.StatusBadRequest, nil)
	var me auth.MeResponse
	ravi.do("POST", "/internal/auth/invites/accept", map[string]string{"token": inviteToken(t, resent.Link), "name": "Ravi", "password": "a long enough password"}, http.StatusOK, &me)
	if !me.TwoFactorSetupRequired {
		t.Fatalf("me = %+v", me)
	}

	// Until Ravi turns it on, the workspace is closed to him.
	ravi.do("GET", "/v1/contacts", nil, http.StatusForbidden, nil)
	var rs auth.TOTPSetup
	ravi.do("POST", "/internal/auth/2fa/setup", nil, http.StatusOK, &rs)
	ravi.do("POST", "/internal/auth/2fa/enable", map[string]string{"secret": rs.Secret, "code": auth.TOTPCode(rs.Secret, time.Now())}, http.StatusOK, &me)
	if me.TwoFactorSetupRequired {
		t.Fatalf("still required: %+v", me)
	}
	ravi.do("GET", "/v1/contacts", nil, http.StatusOK, nil)
}
