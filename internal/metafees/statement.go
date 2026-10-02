// Package metafees is the Solution Partner groundwork (BRD Phase 2): what Meta charges for a
// workspace's messages, by category and country, and the monthly statement that re-bills those
// fees for workspaces that pay Meta through Ecogo. Until Meta approves Ecogo as a Solution
// Partner every workspace pays Meta directly and no statement is issued.
package metafees

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// Payment modes of a workspace.
const (
	ModeDirect     = "direct"
	ModeThroughUs  = "through_us"
	settleDays     = 3 // usage of the last days is still being corrected; see analytics rollupDays
	maxBackMonths  = 12
	statementKeyFY = "MF " // keeps statement numbers apart from invoice numbers
)

// ErrUnrated means usage exists for which the rate card has no rate. The statement waits until a
// rate is added, so a customer is never billed less than Meta charges Ecogo without anyone noticing.
var ErrUnrated = errors.New("usage without a rate in the rate card")

// Card is the rate card.
type Card []dbq.MetaRateCard

// For finds the rate (hundredths of a paisa per message) in force on a day: the country's own rate
// first, then the '*' fallback, the most recent one that has started.
func (c Card) For(category, country string, day time.Time) (int64, bool) {
	var best *dbq.MetaRateCard
	score := func(r *dbq.MetaRateCard) int {
		if r.Country == country {
			return 2
		}
		return 1
	}
	for i := range c {
		r := &c[i]
		if r.Category != category || (r.Country != country && r.Country != "*") || r.EffectiveFrom.Time.After(day) {
			continue
		}
		if best == nil || score(r) > score(best) || (score(r) == score(best) && r.EffectiveFrom.Time.After(best.EffectiveFrom.Time)) {
			best = r
		}
	}
	if best == nil {
		return 0, false
	}
	return best.RateHundredths, true
}

// Line is one row of a statement.
type Line struct {
	Category       string `json:"category"`
	Country        string `json:"country"`
	Messages       int64  `json:"messages"`
	RateHundredths int64  `json:"rate_hundredths"`
	AmountMinor    int64  `json:"amount_minor"`
}

// Usage is Meta's fee for a stretch of days.
type Usage struct {
	Lines    []Line `json:"lines"`
	FeeMinor int64  `json:"fee_minor"`
	Unrated  int64  `json:"unrated_messages"`
}

// Compute prices a workspace's billable messages between two days (inclusive). Run it inside the tenant.
func Compute(ctx context.Context, q *dbq.Queries, card Card, from, to time.Time) (Usage, error) {
	rows, err := q.MetaFeeUsage(ctx, dbq.MetaFeeUsageParams{FromDay: date(from), ToDay: date(to)})
	if err != nil {
		return Usage{}, err
	}
	type key struct {
		category, country string
		rate              int64
	}
	sums := map[key]int64{}
	var u Usage
	for _, r := range rows {
		rate, ok := card.For(r.PricingCategory, r.RecipientCountry, r.Day.Time)
		if !ok {
			u.Unrated += r.Billable
			continue
		}
		sums[key{r.PricingCategory, r.RecipientCountry, rate}] += r.Billable
	}
	for k, n := range sums {
		amount := (k.rate*n + 50) / 100
		u.Lines = append(u.Lines, Line{Category: k.category, Country: k.country, Messages: n, RateHundredths: k.rate, AmountMinor: amount})
		u.FeeMinor += amount
	}
	sort.Slice(u.Lines, func(i, j int) bool {
		a, b := u.Lines[i], u.Lines[j]
		if a.Country != b.Country {
			return a.Country < b.Country
		}
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		return a.RateHundredths < b.RateHundredths
	})
	if u.Lines == nil {
		u.Lines = []Line{}
	}
	return u, nil
}

func date(t time.Time) pgtype.Date {
	return pgtype.Date{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

// Totals is what a statement charges.
type Totals struct {
	MarkupMinor, TaxableMinor, CGST, SGST, IGST, TotalMinor int64
}

// Price adds Ecogo's markup (basis points of Meta's fee) and GST (on top, unlike plan prices) to a fee.
func Price(fee int64, markupBP, gstBP int, sameState bool) Totals {
	t := Totals{MarkupMinor: (fee*int64(markupBP) + 5000) / 10000}
	t.TaxableMinor = fee + t.MarkupMinor
	gst := (t.TaxableMinor*int64(gstBP) + 5000) / 10000
	if sameState {
		t.CGST, t.SGST = gst/2, gst-gst/2
	} else {
		t.IGST = gst
	}
	t.TotalMinor = t.TaxableMinor + gst
	return t
}

// Params are what issuing a statement needs besides the database.
type Params struct {
	Seller   config.Seller
	MarkupBP int
	Now      time.Time
}

// MonthStart is the first day of the month containing t, in the workspace's zone.
func MonthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// Issue creates the statement for one month of a workspace, unless one exists, there was nothing
// to bill, or invoicing is not set up (no seller GSTIN). from is the first day to bill, later than
// the month's first day when the workspace switched to paying through Ecogo mid-month. Run it
// inside the tenant. It returns whether a statement was created.
func Issue(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, loc *time.Location, month, from time.Time, p Params) (bool, error) {
	if p.Seller.GSTIN == "" {
		return false, nil
	}
	if done, err := q.MetaFeeStatementExists(ctx, date(month)); err != nil || done {
		return false, err
	}
	rates, err := q.ListMetaRates(ctx)
	if err != nil {
		return false, err
	}
	end := month.AddDate(0, 1, 0)
	use, err := Compute(ctx, q, Card(rates), from, end.AddDate(0, 0, -1))
	if err != nil {
		return false, err
	}
	if use.Unrated > 0 {
		return false, fmt.Errorf("%w: %d messages in %s", ErrUnrated, use.Unrated, month.Format("2006-01"))
	}
	if use.FeeMinor == 0 {
		return false, nil
	}

	seller := billing.Party{Name: p.Seller.Name, GSTIN: p.Seller.GSTIN, Address: p.Seller.Address, StateCode: p.Seller.GSTIN[:2]}
	buyer := billing.Party{}
	prof, err := q.GetBillingProfile(ctx)
	switch {
	case err == nil:
		g := ""
		if prof.Gstin != nil {
			g = *prof.Gstin
		}
		buyer = billing.Party{Name: prof.LegalName, GSTIN: g, Address: prof.Address, StateCode: prof.StateCode}
	case db.IsNotFound(err):
		t, err := q.GetTenant(ctx, tenantID)
		if err != nil {
			return false, err
		}
		buyer = billing.Party{Name: t.Name}
		if t.LegalName != nil {
			buyer.Name = *t.LegalName
		}
	default:
		return false, err
	}
	place := buyer.StateCode
	if place == "" {
		place = seller.StateCode
	}
	tot := Price(use.FeeMinor, p.MarkupBP, p.Seller.GSTRateBP, place == seller.StateCode)

	// The subscription invoices of the month go on the statement for reference: they are already paid.
	type subLine struct {
		Number     string `json:"number"`
		TotalMinor int64  `json:"total_minor"`
	}
	subs := []subLine{}
	invs, err := q.SubscriptionInvoicesBetween(ctx, dbq.SubscriptionInvoicesBetweenParams{Start: month, EndAt: end})
	if err != nil {
		return false, err
	}
	for _, i := range invs {
		subs = append(subs, subLine{i.Number, i.TotalMinor})
	}

	fy := billing.FinancialYear(p.Now)
	n, err := q.NextInvoiceNumber(ctx, statementKeyFY+fy)
	if err != nil {
		return false, err
	}
	lines, _ := json.Marshal(use.Lines)
	subJSON, _ := json.Marshal(subs)
	sj, _ := json.Marshal(seller)
	bj, _ := json.Marshal(buyer)
	rows, err := q.InsertMetaFeeStatement(ctx, dbq.InsertMetaFeeStatementParams{
		ID: db.NewID(), TenantID: tenantID, Month: date(month), Number: fmt.Sprintf("ECO/MF/%s/%06d", fy, n),
		Lines: lines, Subscription: subJSON, FeeMinor: use.FeeMinor, MarkupBp: int32(p.MarkupBP), MarkupMinor: tot.MarkupMinor,
		TaxableMinor: tot.TaxableMinor, GstRateBp: int32(p.Seller.GSTRateBP), CgstMinor: tot.CGST, SgstMinor: tot.SGST, IgstMinor: tot.IGST,
		TotalMinor: tot.TotalMinor, PlaceOfSupply: place, Seller: sj, Buyer: bj, IssuedAt: p.Now,
	})
	return rows > 0, err
}
