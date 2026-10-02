package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

type rollupKey struct {
	phone    uuid.UUID
	category string
	country  string
	origin   dbq.MessageOrigin
}

// Rollup recomputes usage_daily for one day of the tenant from its messages. day is a calendar
// date in the tenant's time zone. It runs inside the tenant's transaction.
func Rollup(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, loc *time.Location, day time.Time) error {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	rows, err := q.UsageForPeriod(ctx, dbq.UsageForPeriodParams{Start: start, EndAt: start.AddDate(0, 0, 1)})
	if err != nil {
		return err
	}
	sums := map[rollupKey]*dbq.InsertUsageParams{}
	var order []rollupKey
	for _, r := range rows {
		k := rollupKey{r.PhoneNumberID, r.Category, Country(r.Prefix), r.Origin}
		u, ok := sums[k]
		if !ok {
			u = &dbq.InsertUsageParams{
				TenantID: tenantID, PhoneNumberID: k.phone, Day: pgtype.Date{Time: dateOnly(start), Valid: true},
				PricingCategory: k.category, RecipientCountry: k.country, Origin: k.origin,
			}
			sums[k] = u
			order = append(order, k)
		}
		u.Sent += r.Sent
		u.Delivered += r.Delivered
		u.Read += r.Read
		u.Failed += r.Failed
		u.Received += r.Received
		u.Billable += r.Billable
	}
	if err := q.DeleteUsageDay(ctx, pgtype.Date{Time: dateOnly(start), Valid: true}); err != nil {
		return err
	}
	for _, k := range order {
		u := sums[k]
		u.EstMetaCostMinor, _ = Cost(k.country, k.category, u.Billable)
		if err := q.InsertUsage(ctx, *u); err != nil {
			return err
		}
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Location returns the tenant's time zone, or India's when it is not a valid zone name.
func Location(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return india
}

var india = time.FixedZone("IST", 5*3600+1800)
