// Package analytics serves D9 usage reports. Message counts are rolled up per day into
// usage_daily, by number, pricing category, recipient country and origin, so traffic from the
// WhatsApp Business app (coexistence) shows apart from API and campaign traffic. Today and
// yesterday are recomputed whenever the report is opened, and an hourly job keeps the last few
// days current for every tenant, since delivery statuses arrive after the message.
package analytics

import (
	"log/slog"
	"net/http"
	"sort"
	"time"
	_ "time/tzdata" // tenant time zones in a container without zoneinfo

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// maxDays is the longest range one report covers.
const maxDays = 366

type Service struct {
	db  *db.DB
	log *slog.Logger
	now func() time.Time
}

func NewService(d *db.DB, log *slog.Logger) *Service { return &Service{db: d, log: log, now: time.Now} }

// InternalRoutes mounts /internal/analytics for owners and admins.
func (s *Service) InternalRoutes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
	r.Get("/", httpx.Handler(s.log, s.report))
	r.Get("/team", httpx.Handler(s.log, s.team))
}

// Counts are message totals. sent, delivered and read are cumulative.
type Counts struct {
	Sent      int32 `json:"sent"`
	Delivered int32 `json:"delivered"`
	Read      int32 `json:"read"`
	Failed    int32 `json:"failed"`
	Received  int32 `json:"received"`
	Billable  int32 `json:"billable"`
	// Free counts sent messages Meta does not charge for (service replies, free entry points).
	Free int32 `json:"free"`
	// EstCostMinor is Meta's estimated charge in paise.
	EstCostMinor int64 `json:"est_cost_minor"`
}

func (c *Counts) add(u dbq.UsageDaily) {
	c.Sent += u.Sent
	c.Delivered += u.Delivered
	c.Read += u.Read
	c.Failed += u.Failed
	c.Received += u.Received
	c.Billable += u.Billable
	if free := u.Sent - u.Billable; free > 0 {
		c.Free += free
	}
	c.EstCostMinor += u.EstMetaCostMinor
}

type Day struct {
	Day string `json:"day"`
	Counts
}

type Group struct {
	Key string `json:"key"`
	Counts
}

type Report struct {
	From     string `json:"from"`
	To       string `json:"to"`
	TimeZone string `json:"time_zone"`
	Currency string `json:"currency"`
	Totals   Counts `json:"totals"`
	// UnpricedBillable counts billable messages to countries without a rate, which the cost
	// estimate leaves out.
	UnpricedBillable int32   `json:"unpriced_billable"`
	Days             []Day   `json:"days"`
	ByCategory       []Group `json:"by_category"`
	ByOrigin         []Group `json:"by_origin"`
	ByCountry        []Group `json:"by_country"`
	ByNumber         []Group `json:"by_number"`
	// Conversations counts conversations with at least one message in the period, overall and
	// by number ID.
	Conversations         int32            `json:"conversations"`
	ConversationsByNumber map[string]int32 `json:"conversations_by_number"`
}

func (s *Service) report(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	qs := r.URL.Query()
	var phone *uuid.UUID
	if v := qs.Get("phone_number_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("phone_number_id", "phone_number_id is not valid.")
		}
		phone = &id
	}

	var out Report
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err := q.GetTenant(ctx, p.TenantID)
		if err != nil {
			return err
		}
		loc := Location(t.Timezone)
		today := s.now().In(loc)
		from, to, err := dateRange(qs.Get("from"), qs.Get("to"), today)
		if err != nil {
			return err
		}
		// Today's numbers change by the minute and yesterday's statuses are still arriving.
		for _, d := range []time.Time{today, today.AddDate(0, 0, -1)} {
			if err := Rollup(ctx, q, p.TenantID, loc, d); err != nil {
				return err
			}
		}
		rows, err := q.UsageRange(ctx, dbq.UsageRangeParams{
			FromDay: pgtype.Date{Time: from, Valid: true}, ToDay: pgtype.Date{Time: to, Valid: true}, PhoneNumberID: phone,
		})
		if err != nil {
			return err
		}
		out = build(rows, from, to)
		out.TimeZone = loc.String()
		start, end := period(from, to, loc)
		convs, err := q.ConversationsByNumber(ctx, dbq.ConversationsByNumberParams{Start: start, EndAt: end, PhoneNumberID: phone})
		out.ConversationsByNumber = map[string]int32{}
		for _, c := range convs {
			out.Conversations += c.Conversations
			out.ConversationsByNumber[c.PhoneNumberID.String()] = c.Conversations
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// dateRange parses from and to (YYYY-MM-DD, inclusive). The default is the last 30 days.
func dateRange(fromS, toS string, today time.Time) (time.Time, time.Time, error) {
	to := dateOnly(today)
	from := to.AddDate(0, 0, -29)
	var err error
	if toS != "" {
		if to, err = time.Parse(time.DateOnly, toS); err != nil {
			return from, to, httpx.BadRequest("to", "to must be a date like 2026-10-02.")
		}
	}
	if fromS != "" {
		if from, err = time.Parse(time.DateOnly, fromS); err != nil {
			return from, to, httpx.BadRequest("from", "from must be a date like 2026-09-01.")
		}
	} else if toS != "" {
		from = to.AddDate(0, 0, -29)
	}
	if to.Before(from) {
		return from, to, httpx.BadRequest("from", "from must not be after to.")
	}
	if to.Sub(from) >= maxDays*24*time.Hour {
		return from, to, httpx.BadRequest("from", "A report covers at most 366 days.")
	}
	return from, to, nil
}

func build(rows []dbq.UsageDaily, from, to time.Time) Report {
	out := Report{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Currency: "INR"}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out.Days = append(out.Days, Day{Day: d.Format(time.DateOnly)})
	}
	days := map[string]*Day{}
	for i := range out.Days {
		days[out.Days[i].Day] = &out.Days[i]
	}
	cat, orig, country, num := map[string]*Counts{}, map[string]*Counts{}, map[string]*Counts{}, map[string]*Counts{}
	group := func(m map[string]*Counts, k string, u dbq.UsageDaily) {
		if m[k] == nil {
			m[k] = &Counts{}
		}
		m[k].add(u)
	}
	for _, u := range rows {
		out.Totals.add(u)
		if d := days[u.Day.Time.Format(time.DateOnly)]; d != nil {
			d.add(u)
		}
		group(cat, u.PricingCategory, u)
		group(orig, string(u.Origin), u)
		group(country, u.RecipientCountry, u)
		group(num, u.PhoneNumberID.String(), u)
		if _, ok := Cost(u.RecipientCountry, u.PricingCategory, u.Billable); !ok {
			out.UnpricedBillable += u.Billable
		}
	}
	out.ByCategory, out.ByOrigin, out.ByCountry, out.ByNumber = sorted(cat), sorted(orig), sorted(country), sorted(num)
	return out
}

// sorted lists groups with the most traffic first.
func sorted(m map[string]*Counts) []Group {
	out := make([]Group, 0, len(m))
	for k, c := range m {
		out = append(out, Group{Key: k, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Sent+out[i].Received, out[j].Sent+out[j].Received
		if a != b {
			return a > b
		}
		return out[i].Key < out[j].Key
	})
	return out
}
