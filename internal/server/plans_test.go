package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// The standard plans come with the migrations. Free is offered at once; the paid plans wait
// for their Razorpay plans.
func TestStandardPlans(t *testing.T) {
	h := newSeededHarness(t)
	owner := h.newClient()
	owner.signup("owner@example.com", "Sharma Sweets")
	staff := h.platformAdmin("ops@ecogo.co.in")

	var all struct{ Data []billing.Plan }
	staff.do("GET", "/internal/admin/plans", nil, http.StatusOK, &all)
	want := []struct {
		code                       string
		price                      int64
		numbers, seats, cont, msgs int32
	}{
		{"free", 0, 1, 1, 25, 50}, {"starter", 99900, 1, 2, 2500, 2500},
		{"growth", 249900, 3, 8, 15000, 15000}, {"scale", 499900, 10, 25, 75000, 75000},
	}
	if len(all.Data) != len(want) {
		t.Fatalf("plans = %+v", all.Data)
	}
	for i, w := range want {
		p := all.Data[i]
		if p.Code != w.code || p.PriceMinor != w.price || p.IncludedNumbers != w.numbers || p.IncludedSeats != w.seats ||
			*p.ContactsLimit != w.cont || *p.MessagesPerMonth != w.msgs || len(p.Highlights) == 0 || p.Free != (w.price == 0) {
			t.Fatalf("plan %d = %+v", i, p)
		}
	}
	if !all.Data[2].Popular || all.Data[1].Popular {
		t.Fatalf("growth should be the popular plan: %+v", all.Data)
	}

	var ov billing.Overview
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if len(ov.Plans) != 1 || ov.Plans[0].Code != "free" {
		t.Fatalf("offered = %+v", ov.Plans)
	}
}

func TestFreePlanLimits(t *testing.T) {
	h := newHarness(t)
	owner, me, phone := h.connected()
	staff := h.platformAdmin("ops@ecogo.co.in")
	staff.do("PUT", "/internal/admin/plans/free", map[string]any{"name": "Free", "price_minor": 0, "included_numbers": 1,
		"included_seats": 1, "is_active": true, "contacts_limit": 3, "messages_per_month": 2,
		"tagline": " Try it ", "highlights": []string{"Inbox", " ", "Templates"}}, http.StatusOK, nil)
	staff.do("PUT", "/internal/admin/plans/starter", map[string]any{"name": "Starter", "price_minor": 99900, "included_numbers": 1,
		"included_seats": 2, "is_active": true, "razorpay_plan_id": "plan_S1", "yearly_price_minor": 999000,
		"yearly_razorpay_plan_id": "plan_S12", "contacts_limit": 2500, "messages_per_month": 2500}, http.StatusOK, nil)

	var ov billing.Overview
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if len(ov.Plans) != 2 || ov.Plans[0].Code != "free" || ov.Plans[0].Tagline != "Try it" || len(ov.Plans[0].Highlights) != 2 ||
		!ov.Plans[1].YearlyAvailable || ov.Plans[1].YearlyRazorpayPlanID != nil || *ov.Plans[1].YearlyPriceMinor != 999000 {
		t.Fatalf("plans = %+v", ov.Plans)
	}

	// The free plan starts without Razorpay, even while payments are switched on.
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "free", "interval": "week"}, http.StatusBadRequest, nil)
	calls := len(h.razorpay.calls)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "free"}, http.StatusOK, nil)
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Subscription.Status != "active" || ov.Plan == nil || ov.Plan.Code != "free" || !ov.Subscription.Usable || len(h.razorpay.calls) != calls {
		t.Fatalf("free = %+v", ov)
	}
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "free"}, http.StatusConflict, nil)

	// Contacts: three in all. Updating one that exists is never refused.
	for _, wa := range []string{"919800000001", "919800000002", "919800000003"} {
		owner.do("POST", "/v1/contacts", map[string]any{"wa_id": wa}, http.StatusCreated, nil)
	}
	var e apiErr
	owner.do("POST", "/v1/contacts", map[string]any{"wa_id": "919800000004"}, http.StatusConflict, &e)
	if e.Error.Code != "contact_limit" {
		t.Fatalf("over contacts = %+v", e)
	}
	owner.do("POST", "/v1/contacts", map[string]any{"wa_id": "919800000001", "name": "Asha"}, http.StatusOK, nil)
	var res struct {
		Created, Updated, Skipped int
	}
	owner.importCSV("phone,name\n919800000002,Ravi\n919800000005,Meena\n", false, http.StatusOK, &res)
	if res.Created != 0 || res.Updated != 1 || res.Skipped != 1 {
		t.Fatalf("import = %+v", res)
	}

	// Messages: two a month. A customer writing in still arrives.
	h.inbound(customer, "wamid.free1", "Hello")
	owner.send(text(phone.ID, "one"), "", http.StatusAccepted, nil)
	owner.send(text(phone.ID, "two"), "", http.StatusAccepted, nil)
	owner.send(text(phone.ID, "three"), "", http.StatusPaymentRequired, &e)
	if e.Error.Code != "message_limit" {
		t.Fatalf("over messages = %+v", e)
	}
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.MessagesThisMonth != 2 || ov.Contacts != 3 {
		t.Fatalf("usage = %+v", ov)
	}

	// Next month the allowance is back.
	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE messages SET created_at = created_at - interval '40 days'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	owner.send(text(phone.ID, "new month"), "", http.StatusAccepted, nil)

	// Upgrading to a yearly plan from the free plan: Razorpay charges the yearly plan now, and the
	// free plan carries on until Razorpay confirms.
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "starter", "interval": "year"}, http.StatusOK, nil)
	call, body := h.razorpay.last()
	if call != "POST /v1/subscriptions" || body["plan_id"] != "plan_S12" || body["total_count"] != float64(10) || body["start_at"] != nil {
		t.Fatalf("yearly checkout = %s %v", call, body)
	}
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Plan.Code != "free" || !ov.Subscription.PaymentPending {
		t.Fatalf("pending upgrade = %+v", ov)
	}
	start, end := time.Now().Add(-time.Hour).Unix(), time.Now().AddDate(1, 0, 0).Unix()
	sub := map[string]any{"id": "sub_1", "plan_id": "plan_S12", "status": "active", "current_start": start, "current_end": end}
	h.razorpayEvent("evt_y1", "subscription.charged", sub, "whsec")
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Plan.Code != "starter" || ov.Subscription.Interval != "year" || ov.Subscription.PaymentPending ||
		ov.MonthEnd.After(time.Now().AddDate(0, 1, 1)) {
		t.Fatalf("yearly = %+v", ov)
	}
	// A paid plan has to run out before the free plan can be chosen.
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "free"}, http.StatusConflict, nil)
}

// A checkout started from the free plan and abandoned leaves the workspace on the free plan.
func TestFreePlanAbandonedCheckout(t *testing.T) {
	h := newHarness(t)
	owner, _, _ := h.connected()
	staff := h.platformAdmin("ops@ecogo.co.in")
	staff.do("PUT", "/internal/admin/plans/free", map[string]any{"name": "Free", "price_minor": 0, "included_numbers": 1,
		"included_seats": 1, "is_active": true}, http.StatusOK, nil)
	staff.do("PUT", "/internal/admin/plans/starter", map[string]any{"name": "Starter", "price_minor": 99900, "included_numbers": 1,
		"included_seats": 2, "is_active": true, "razorpay_plan_id": "plan_S1"}, http.StatusOK, nil)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "free"}, http.StatusOK, nil)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "starter", "interval": "year"}, http.StatusBadRequest, nil)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "starter"}, http.StatusOK, nil)
	h.razorpayEvent("evt_a1", "subscription.expired", map[string]any{"id": "sub_1", "plan_id": "plan_S1"}, "whsec")
	var ov billing.Overview
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Subscription.Status != "active" || ov.Plan == nil || ov.Plan.Code != "free" || ov.Subscription.PaymentPending {
		t.Fatalf("after abandoned checkout = %+v", ov)
	}

	// Cancelling a pending checkout from the free plan stops it at once.
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "starter"}, http.StatusOK, nil)
	owner.do("POST", "/internal/billing/cancel", nil, http.StatusOK, nil)
	call, body := h.razorpay.last()
	if call != "POST /v1/subscriptions/sub_2/cancel" || body["cancel_at_cycle_end"] != float64(0) {
		t.Fatalf("cancel = %s %v", call, body)
	}
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if ov.Subscription.Status != "active" || ov.Plan.Code != "free" || ov.Subscription.PaymentPending || ov.Subscription.CancelAtPeriodEnd {
		t.Fatalf("after cancel = %+v", ov)
	}
}

func TestUsageMonth(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse(time.DateOnly, s); return v }
	for _, c := range []struct{ start, now, from, to string }{
		{"2026-01-15", "2026-01-20", "2026-01-15", "2026-02-15"},
		{"2026-01-15", "2026-03-14", "2026-02-15", "2026-03-15"},
		{"2026-01-15", "2026-03-15", "2026-03-15", "2026-04-15"},
		{"2025-11-10", "2026-10-31", "2026-10-10", "2026-11-10"},
	} {
		from, to := billing.UsageMonth(dbq.Subscription{CurrentPeriodStart: at(c.start)}, at(c.now))
		if !from.Equal(at(c.from)) || !to.Equal(at(c.to)) {
			t.Errorf("UsageMonth(%s, %s) = %s, %s", c.start, c.now, from, to)
		}
	}
}
