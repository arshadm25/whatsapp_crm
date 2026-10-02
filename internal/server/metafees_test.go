package server_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/metafees"
)

// usage adds billable messages to a workspace's daily usage.
func (h *harness) usage(tenant, phone uuid.UUID, day, category, country string, billable int) {
	h.t.Helper()
	err := h.db.InTenant(context.Background(), tenant, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO usage_daily (tenant_id, phone_number_id, day, pricing_category, recipient_country, origin, sent, billable)
			VALUES ($1, $2, $3, $4, $5, 'api', $6, $6)`, tenant, phone, day, category, country, billable)
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) runStatements(now time.Time) {
	h.t.Helper()
	w := h.metaFees.At(func() time.Time { return now })
	if err := w.Work(context.Background(), &river.Job[metafees.StatementArgs]{}); err != nil {
		h.t.Fatal(err)
	}
}

func TestMetaFeeStatements(t *testing.T) {
	h := newHarness(t)
	owner, me, phone := h.connected()
	tenant := me.Tenant.ID
	staff := h.platformAdmin("ops@ecogo.co.in")
	phoneID := phone.ID

	// Usage: August before and after the switch, September with a country that has no rate yet.
	h.usage(tenant, phoneID, "2026-08-10", "marketing", "IN", 500) // before the switch: the workspace's own Meta bill
	h.usage(tenant, phoneID, "2026-08-20", "marketing", "IN", 100)
	h.usage(tenant, phoneID, "2026-09-05", "marketing", "IN", 200)
	h.usage(tenant, phoneID, "2026-09-06", "utility", "IN", 100)
	h.usage(tenant, phoneID, "2026-09-07", "marketing", "US", 10)
	h.usage(tenant, phoneID, "2026-10-02", "marketing", "IN", 40) // this month, not finished

	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	var fees metafees.Overview
	owner.do("GET", "/internal/billing/meta-fees", nil, http.StatusOK, &fees)
	if fees.Mode != "direct" || fees.MonthToDate != nil || len(fees.Statements) != 0 || !fees.Invoicing {
		t.Fatalf("new workspace = %+v", fees)
	}
	h.runStatements(now) // paying Meta directly: nothing is billed
	var n int
	h.scalar(tenant, "SELECT count(*) FROM meta_fee_statements", &n)
	if n != 0 {
		t.Fatalf("statements for a direct workspace = %d", n)
	}

	// Only platform admins switch the mode, with a reason.
	body := map[string]any{"mode": "through_us", "reason": "Onboarded before approval, pays us"}
	owner.do("POST", "/internal/admin/tenants/"+tenant.String()+"/meta-payment-mode", body, http.StatusNotFound, nil)
	staff.do("POST", "/internal/admin/tenants/"+tenant.String()+"/meta-payment-mode", map[string]any{"mode": "free", "reason": "Onboarded before approval"}, http.StatusBadRequest, nil)
	staff.do("POST", "/internal/admin/tenants/"+tenant.String()+"/meta-payment-mode", map[string]any{"mode": "through_us", "reason": "no"}, http.StatusBadRequest, nil)
	staff.do("POST", "/internal/admin/tenants/"+tenant.String()+"/meta-payment-mode", body, http.StatusOK, nil)
	err := h.db.Global(context.Background(), func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE tenants SET meta_payment_mode_since = '2026-08-15T00:00:00+05:30' WHERE id = $1", tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// August bills from the 15th; September waits because the US has no rate.
	h.runStatements(now)
	owner.do("GET", "/internal/billing/meta-fees", nil, http.StatusOK, &fees)
	if fees.Mode != "through_us" || len(fees.Statements) != 1 || fees.MonthToDate == nil || fees.MonthToDate.Month != "2026-10" {
		t.Fatalf("after first run = %+v", fees)
	}
	aug := fees.Statements[0]
	// 100 marketing messages at Rs 0.7846 = Rs 78.46; 5% service charge 3.92 (rounded down from 3.923); GST 18% on 82.38.
	if aug.Month != "2026-08" || aug.FeeMinor != 7846 || aug.MarkupMinor != 392 || aug.TaxableMinor != 8238 || aug.GSTMinor != 1483 || aug.TotalMinor != 9721 || aug.Status != "due" {
		t.Fatalf("august = %+v", aug)
	}
	if fees.MonthToDate.FeeMinor != 3138 || fees.MonthToDate.Unrated != 0 { // 40 x 0.7846 = 31.38
		t.Fatalf("month to date = %+v", fees.MonthToDate)
	}

	// Rates are set by platform admins; a US rate releases September.
	var rates struct {
		Data []struct {
			ID, Category, Country string
		}
	}
	staff.do("GET", "/internal/admin/meta-rates", nil, http.StatusOK, &rates)
	if len(rates.Data) != 3 {
		t.Fatalf("seeded rates = %+v", rates)
	}
	for _, bad := range []map[string]any{
		{"category": "service", "country": "US", "rate_hundredths": 1, "effective_from": "2026-01-01"},
		{"category": "marketing", "country": "usa", "rate_hundredths": 1, "effective_from": "2026-01-01"},
		{"category": "marketing", "country": "US", "rate_hundredths": -1, "effective_from": "2026-01-01"},
		{"category": "marketing", "country": "US", "rate_hundredths": 1, "effective_from": "soon"},
	} {
		staff.do("PUT", "/internal/admin/meta-rates", bad, http.StatusBadRequest, nil)
	}
	owner.do("PUT", "/internal/admin/meta-rates", map[string]any{"category": "marketing", "country": "US", "rate_hundredths": 3000, "effective_from": "2000-01-01"}, http.StatusNotFound, nil)
	staff.do("PUT", "/internal/admin/meta-rates", map[string]any{"category": "marketing", "country": "US", "rate_hundredths": 3000, "effective_from": "2000-01-01"}, http.StatusOK, nil)
	h.runStatements(now)
	h.runStatements(now) // issuing again changes nothing
	owner.do("GET", "/internal/billing/meta-fees", nil, http.StatusOK, &fees)
	if len(fees.Statements) != 2 {
		t.Fatalf("statements = %+v", fees.Statements)
	}
	sep := fees.Statements[0]
	// 200 x 0.7846 + 100 x 0.115 + 10 x 0.30 = 156.92 + 11.50 + 3.00 = 171.42; 5% = 8.57; taxable 179.99; GST 32.40.
	if sep.Month != "2026-09" || sep.FeeMinor != 17142 || sep.MarkupMinor != 857 || sep.TaxableMinor != 17999 || sep.GSTMinor != 3240 || sep.TotalMinor != 21239 || len(sep.Lines) != 3 {
		t.Fatalf("september = %+v", sep)
	}
	if !strings.HasPrefix(sep.Number, "ECO/MF/2026-27/") || sep.Number == aug.Number {
		t.Fatalf("numbers %s, %s", aug.Number, sep.Number)
	}

	// The statement is a print-ready page with the split and the fees by category and country.
	status, page := owner.page("/internal/billing/meta-fees/statements/" + sep.ID.String() + "/view")
	for _, want := range []string{"Monthly statement", sep.Number, "Sharma Sweets", "GSTIN 32AABCE1234F1Z5", "CGST @ 9%", "SGST @ 9%", "171.42", "8.57", "179.99", "212.39", "Marketing", "US"} {
		if status != http.StatusOK || !strings.Contains(page, want) {
			t.Fatalf("statement page (%d) lacks %q:\n%s", status, want, page)
		}
	}
	if status, _ := owner.page("/internal/billing/meta-fees/statements/" + uuid.NewString() + "/view"); status != http.StatusNotFound {
		t.Fatalf("unknown statement = %d", status)
	}

	// Platform admins see what is owed across workspaces and record payments.
	var all struct {
		Data     []metafees.Statement
		DueMinor int64 `json:"due_minor"`
	}
	staff.do("GET", "/internal/admin/meta-fee-statements?status=due", nil, http.StatusOK, &all)
	if len(all.Data) != 2 || all.DueMinor != aug.TotalMinor+sep.TotalMinor || all.Data[0].TenantName == nil {
		t.Fatalf("admin list = %+v", all)
	}
	pay := "/internal/admin/meta-fee-statements/" + sep.ID.String() + "/status"
	staff.do("POST", pay, map[string]any{"status": "paid", "reason": "Received by UPI"}, http.StatusBadRequest, nil)
	staff.do("POST", pay, map[string]any{"status": "paid", "payment_reference": "UPI123456", "reason": "Received by UPI"}, http.StatusOK, nil)
	staff.do("GET", "/internal/admin/meta-fee-statements?status=due", nil, http.StatusOK, &all)
	if len(all.Data) != 1 || all.DueMinor != aug.TotalMinor {
		t.Fatalf("after payment = %+v", all)
	}
	owner.do("GET", "/internal/billing/meta-fees", nil, http.StatusOK, &fees)
	if fees.Statements[0].Status != "paid" || fees.Statements[0].PaymentReference == nil || *fees.Statements[0].PaymentReference != "UPI123456" {
		t.Fatalf("paid = %+v", fees.Statements[0])
	}
	_, adminPage := staff.page("/internal/admin/meta-fee-statements/" + sep.ID.String() + "/view")
	if !strings.Contains(adminPage, "UPI123456") {
		t.Fatalf("admin page lacks the payment reference:\n%s", adminPage)
	}

	// Switching back to direct stops new statements; the audit log keeps who and why.
	staff.do("POST", "/internal/admin/tenants/"+tenant.String()+"/meta-payment-mode", map[string]any{"mode": "direct", "reason": "Approved as Tech Provider only"}, http.StatusOK, nil)
	var audited int
	h.scalar(tenant, fmt.Sprintf("SELECT count(*) FROM audit_log WHERE action IN ('tenant.meta_payment_mode', 'meta_fee_statement.paid') AND tenant_id = '%s'", tenant), &audited)
	if audited != 3 {
		t.Fatalf("audit entries = %d", audited)
	}
}

func TestMetaRateCard(t *testing.T) {
	card := metafees.Card{}
	if _, ok := card.For("marketing", "IN", time.Now()); ok {
		t.Fatal("empty card has a rate")
	}
	// Exercised with the database in TestMetaFeeStatements; here the pricing arithmetic.
	tot := metafees.Price(10000, 500, 1800, true)
	if tot.MarkupMinor != 500 || tot.TaxableMinor != 10500 || tot.CGST != 945 || tot.SGST != 945 || tot.TotalMinor != 12390 {
		t.Fatalf("intra = %+v", tot)
	}
	tot = metafees.Price(10000, 0, 1800, false)
	if tot.IGST != 1800 || tot.CGST != 0 || tot.TotalMinor != 11800 {
		t.Fatalf("inter = %+v", tot)
	}
}
