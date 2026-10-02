package server_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
)

// fakeRazorpay records Razorpay API calls and answers like the real one.
type fakeRazorpay struct {
	mu     sync.Mutex
	calls  []string
	bodies []map[string]any
	n      int
}

func (f *fakeRazorpay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != "rzp_test" || p != "rzp_secret" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"BAD_REQUEST_ERROR","description":"Authentication failed"}}`)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.bodies = append(f.bodies, body)
	switch {
	case r.URL.Path == "/v1/customers":
		_, _ = io.WriteString(w, `{"id":"cust_1"}`)
	case r.URL.Path == "/v1/subscriptions":
		f.n++
		fmt.Fprintf(w, `{"id":"sub_%d","plan_id":%q,"status":"created","short_url":"https://rzp.io/i/sub%d"}`, f.n, body["plan_id"], f.n)
	default:
		id := strings.Split(r.URL.Path, "/")[3]
		fmt.Fprintf(w, `{"id":%q,"status":"active"}`, id)
	}
}

func (f *fakeRazorpay) last() (string, map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1], f.bodies[len(f.bodies)-1]
}

// razorpayEvent delivers a signed subscription webhook and returns the status code.
func (h *harness) razorpayEvent(id, event string, sub map[string]any, secret string) int {
	h.t.Helper()
	body, _ := json.Marshal(map[string]any{"event": event, "payload": map[string]any{"subscription": map[string]any{"entity": sub}}})
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	req, _ := http.NewRequest("POST", h.api.URL+"/webhooks/razorpay", bytes.NewReader(body))
	req.Header.Set("X-Razorpay-Signature", hex.EncodeToString(m.Sum(nil)))
	req.Header.Set("X-Razorpay-Event-Id", id)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// platformAdmin is a staff client with two-step verification on.
func (h *harness) platformAdmin(email string) *client {
	h.t.Helper()
	c := h.newClient()
	c.signup(email, "Ecogo")
	err := h.db.Global(context.Background(), func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.SetPlatformAdmin(context.Background(), dbq.SetPlatformAdminParams{Email: email, IsPlatformAdmin: true})
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	var setup auth.TOTPSetup
	c.do("POST", "/internal/auth/2fa/setup", nil, http.StatusOK, &setup)
	c.do("POST", "/internal/auth/2fa/enable", map[string]string{"secret": setup.Secret, "code": auth.TOTPCode(setup.Secret, time.Now())}, http.StatusOK, nil)
	return c
}

func TestBilling(t *testing.T) {
	h := newHarness(t)
	owner, me, phone := h.connected()
	staff := h.platformAdmin("ops@ecogo.co.in")

	var ov billing.Overview
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	trialEnd := ov.Subscription.PeriodEnd
	if ov.Subscription.Status != "trialing" || !ov.Subscription.Usable || len(ov.Plans) != 0 || !ov.PaymentsEnabled ||
		ov.ConnectedNumber != 1 || ov.Seats != 1 || time.Until(trialEnd) < 13*24*time.Hour {
		t.Fatalf("new workspace billing = %+v", ov)
	}

	// Admins set the plans; a plan without its Razorpay plan cannot be bought yet.
	growth := map[string]any{"name": "Growth", "price_minor": 299900, "included_numbers": 1, "included_seats": 5,
		"extra_seat_minor": 49900, "is_active": true, "sort_order": 2}
	staff.do("PUT", "/internal/admin/plans/Growth", growth, http.StatusBadRequest, nil)
	staff.do("PUT", "/internal/admin/plans/growth", growth, http.StatusOK, nil)
	owner.do("PUT", "/internal/admin/plans/growth", growth, http.StatusNotFound, nil)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "growth"}, http.StatusBadRequest, nil)
	growth["razorpay_plan_id"] = "plan_G1"
	staff.do("PUT", "/internal/admin/plans/growth", growth, http.StatusOK, nil)
	staff.do("PUT", "/internal/admin/plans/pro", map[string]any{"name": "Pro", "price_minor": 799900, "included_numbers": 3,
		"included_seats": 15, "extra_seat_minor": 39900, "is_active": true, "razorpay_plan_id": "plan_P1", "sort_order": 3}, http.StatusOK, nil)
	staff.do("PUT", "/internal/admin/plans/legacy", map[string]any{"name": "Legacy", "price_minor": 100, "included_numbers": 1,
		"included_seats": 1, "is_active": false}, http.StatusOK, nil)
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if len(ov.Plans) != 2 || ov.Plans[0].Code != "growth" || ov.Plans[1].Code != "pro" || ov.Plans[0].RazorpayPlanID != nil {
		t.Fatalf("plans = %+v", ov.Plans)
	}

	// Subscribing during the trial: the first charge waits for the trial's end.
	var checkout struct {
		PaymentURL string `json:"payment_url"`
	}
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "legacy"}, http.StatusBadRequest, nil)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "growth"}, http.StatusOK, &checkout)
	call, body := h.razorpay.last()
	if checkout.PaymentURL != "https://rzp.io/i/sub1" || call != "POST /v1/subscriptions" || body["plan_id"] != "plan_G1" ||
		int64(body["start_at"].(float64)) != trialEnd.Unix() || body["customer_id"] != "cust_1" {
		t.Fatalf("checkout = %+v, %s %v", checkout, call, body)
	}
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if !ov.Subscription.PaymentPending {
		t.Fatalf("pending = %+v", ov.Subscription)
	}
	// Checking out again cancels the first, unpaid subscription before creating a new one.
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "growth"}, http.StatusOK, &checkout)
	h.razorpay.mu.Lock()
	again := h.razorpay.calls[len(h.razorpay.calls)-2:]
	cancelBody := h.razorpay.bodies[len(h.razorpay.bodies)-2]
	h.razorpay.mu.Unlock()
	if again[0] != "POST /v1/subscriptions/sub_1/cancel" || cancelBody["cancel_at_cycle_end"] != float64(0) ||
		again[1] != "POST /v1/subscriptions" || checkout.PaymentURL != "https://rzp.io/i/sub2" {
		t.Fatalf("second checkout = %v %v %+v", again, cancelBody, checkout)
	}

	// Razorpay's webhooks drive the state; bad signatures and repeats are ignored.
	sub := map[string]any{"id": "sub_2", "plan_id": "plan_G1", "status": "authenticated"}
	if code := h.razorpayEvent("evt_1", "subscription.authenticated", sub, "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("bad signature = %d", code)
	}
	if code := h.razorpayEvent("evt_1", "subscription.authenticated", sub, "whsec"); code != http.StatusOK {
		t.Fatalf("authenticated = %d", code)
	}
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Subscription.Status != "trialing" || ov.Plan == nil || ov.Plan.Code != "growth" || ov.Subscription.PaymentPending {
		t.Fatalf("after authenticated = %+v", ov)
	}
	start, end := time.Now().Add(-time.Hour).Unix(), time.Now().Add(30*24*time.Hour).Unix()
	charged := map[string]any{"id": "sub_2", "plan_id": "plan_G1", "status": "active", "current_start": start, "current_end": end}
	h.razorpayEvent("evt_2", "subscription.charged", charged, "whsec")
	h.razorpayEvent("evt_3", "subscription.pending", charged, "whsec")
	h.razorpayEvent("evt_2", "subscription.charged", charged, "whsec") // a retry of evt_2 changes nothing
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Subscription.Status != "past_due" || !ov.Subscription.Usable || ov.Subscription.PeriodEnd.Unix() != end {
		t.Fatalf("after pending = %+v", ov.Subscription)
	}
	h.razorpayEvent("evt_4", "subscription.charged", charged, "whsec")
	h.razorpayEvent("evt_x", "subscription.charged", map[string]any{"id": "sub_unknown"}, "whsec")

	// Plan changes apply from the next cycle.
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "growth"}, http.StatusConflict, nil)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "pro"}, http.StatusOK, nil)
	call, body = h.razorpay.last()
	if call != "PATCH /v1/subscriptions/sub_2" || body["plan_id"] != "plan_P1" || body["schedule_change_at"] != "cycle_end" {
		t.Fatalf("change plan = %s %v", call, body)
	}

	// Cancelling keeps the paid month; once Razorpay ends it, sending stops.
	owner.do("POST", "/internal/billing/cancel", nil, http.StatusOK, nil)
	call, body = h.razorpay.last()
	if call != "POST /v1/subscriptions/sub_2/cancel" || body["cancel_at_cycle_end"] != float64(1) {
		t.Fatalf("cancel = %s %v", call, body)
	}
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if !ov.Subscription.CancelAtPeriodEnd || ov.Subscription.Status != "active" {
		t.Fatalf("after cancel = %+v", ov.Subscription)
	}
	h.inbound(customer, "wamid.bill1", "Is my order ready?")
	owner.send(text(phone.ID, "still on"), "", http.StatusAccepted, nil)
	h.razorpayEvent("evt_5", "subscription.cancelled", map[string]any{"id": "sub_2", "plan_id": "plan_G1", "ended_at": time.Now().Add(-time.Minute).Unix()}, "whsec")
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Subscription.Status != "cancelled" || ov.Subscription.Usable {
		t.Fatalf("after cancelled = %+v", ov.Subscription)
	}
	var e apiErr
	owner.send(text(phone.ID, "blocked"), "", http.StatusPaymentRequired, &e)
	if e.Error.Code != "payment_required" {
		t.Fatalf("send after end = %+v", e)
	}

	// Admins see the subscription and can extend a trial, only while it is a trial.
	var detail struct {
		Subscription billing.Subscription `json:"subscription"`
	}
	tenant := "/internal/admin/tenants/" + me.Tenant.ID.String()
	staff.do("GET", tenant, nil, http.StatusOK, &detail)
	if detail.Subscription.Status != "cancelled" {
		t.Fatalf("admin sees = %+v", detail.Subscription)
	}
	staff.do("POST", tenant+"/extend-trial", map[string]any{"days": 7, "reason": "Meta verification pending"}, http.StatusConflict, nil)

	other := h.newClient()
	otherMe := other.signup("new@shop.in", "New Shop")
	other.do("POST", "/internal/billing/cancel", nil, http.StatusConflict, nil)
	var extended billing.Subscription
	staff.do("POST", "/internal/admin/tenants/"+otherMe.Tenant.ID.String()+"/extend-trial", map[string]any{"days": 0, "reason": "x"}, http.StatusBadRequest, nil)
	staff.do("POST", "/internal/admin/tenants/"+otherMe.Tenant.ID.String()+"/extend-trial", map[string]any{"days": 7, "reason": "Meta verification pending"}, http.StatusOK, &extended)
	if time.Until(extended.PeriodEnd) < 20*24*time.Hour {
		t.Fatalf("extended = %+v", extended)
	}
}

func TestBillingIsOwnerOnly(t *testing.T) {
	h := newHarness(t)
	owner := h.newClient()
	owner.signup("owner@example.com", "Sharma Sweets")
	var inv auth.Invite
	owner.do("POST", "/internal/team/invites", map[string]string{"email": "admin@example.com", "role": "admin"}, http.StatusCreated, &inv)
	admin := h.newClient()
	admin.do("POST", "/internal/auth/invites/accept", map[string]string{"token": inviteToken(t, inv.Link), "name": "Ravi", "password": "a long enough password"}, http.StatusOK, nil)
	admin.do("GET", "/internal/billing", nil, http.StatusForbidden, nil)
}

// connect runs Embedded Signup for one more number of the workspace's WABA.
func (h *harness) connect(c *client, tenant uuid.UUID, metaID string) onboarding.SessionView {
	h.t.Helper()
	var sess onboarding.SessionView
	c.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusCreated, &sess)
	c.do("POST", "/internal/onboarding/sessions/"+sess.ID.String()+"/complete", map[string]string{
		"code": "c" + metaID, "waba_id": "1100111", "phone_number_id": metaID}, http.StatusOK, &sess)
	return sess
}

func TestPlanNumberLimit(t *testing.T) {
	h := newHarness(t)
	owner, me, _ := h.connected()
	staff := h.platformAdmin("ops@ecogo.co.in")
	h.meta.numbers["555002"] = "+91 90000 00002"
	h.meta.numbers["555003"] = "+91 90000 00003"

	// During the trial no plan applies, so a second number connects.
	second := h.connect(owner, me.Tenant.ID, "555002")
	if err := h.runJob(me.Tenant.ID, second.ID, 1); err != nil {
		t.Fatalf("second number: %v", err)
	}
	pending := h.connect(owner, me.Tenant.ID, "555003") // started before a plan is chosen

	plan := func(code string, numbers int) {
		staff.do("PUT", "/internal/admin/plans/"+code, map[string]any{"name": code, "price_minor": 100000, "included_numbers": numbers,
			"included_seats": 5, "extra_seat_minor": 10000, "is_active": true, "razorpay_plan_id": "plan_" + code}, http.StatusOK, nil)
	}
	plan("solo", 1)
	plan("duo", 2)

	// A plan that includes fewer numbers than are connected cannot be chosen.
	var e apiErr
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "solo"}, http.StatusConflict, &e)
	if e.Error.Code != "plan_limit" {
		t.Fatalf("solo = %+v", e)
	}
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "duo"}, http.StatusOK, nil)
	h.razorpayEvent("evt_a", "subscription.authenticated", map[string]any{"id": "sub_1", "plan_id": "plan_duo", "status": "authenticated"}, "whsec")

	// Both numbers are in use now: starting another is refused up front, and one already
	// started stops at the step that would save it.
	owner.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusConflict, &e)
	if e.Error.Code != "plan_limit" {
		t.Fatalf("start = %+v", e)
	}
	_ = h.runJob(me.Tenant.ID, pending.ID, 1)
	owner.do("GET", "/internal/onboarding/sessions/"+pending.ID.String(), nil, http.StatusOK, &pending)
	if pending.State != "failed" || pending.Error == nil || pending.Error.Code != "plan_limit" {
		t.Fatalf("pending session = %+v", pending)
	}
	var list struct{ Data []numbers.PhoneNumber }
	owner.do("GET", "/v1/phone-numbers", nil, http.StatusOK, &list)
	if len(list.Data) != 2 {
		t.Fatalf("numbers = %d, want 2", len(list.Data))
	}
}
