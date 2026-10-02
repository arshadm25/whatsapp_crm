package metafees

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

// BillingMeta is the part of the Meta client that billing needs.
type BillingMeta interface {
	PricingAnalytics(ctx context.Context, token, wabaID string, start, end time.Time) ([]metaclient.PricingPoint, error)
	AttachCreditLine(ctx context.Context, token, creditLineID, wabaID, currency string) (string, error)
}

const (
	// reconcileDays is how many finished days each run checks again; Meta's numbers settle for a day or two.
	reconcileDays = 5
	// Counts differ by up to this much (or 1%) before they are called a mismatch.
	messageTolerance = 2
)

// ReconcileArgs is the daily job that compares our usage with Meta's billing data.
type ReconcileArgs struct{}

func (ReconcileArgs) Kind() string { return "reconcile_meta_billing" }

func (ReconcileArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 3}
}

// ReconcilePeriodic returns the daily job.
func ReconcilePeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return ReconcileArgs{}, nil },
		&river.PeriodicJobOpts{ID: "reconcile_meta_billing", RunOnStart: false})
}

type Reconciler struct {
	river.WorkerDefaults[ReconcileArgs]
	db   *db.DB
	keys *envelope.Keyring
	meta BillingMeta
	log  *slog.Logger
	now  func() time.Time
}

func NewReconciler(d *db.DB, keys *envelope.Keyring, meta BillingMeta, log *slog.Logger) *Reconciler {
	return &Reconciler{db: d, keys: keys, meta: meta, log: log, now: time.Now}
}

// At makes the reconciler read the time from now; tests use it.
func (r *Reconciler) At(now func() time.Time) *Reconciler {
	r.now = now
	return r
}

func (r *Reconciler) Timeout(*river.Job[ReconcileArgs]) time.Duration { return 30 * time.Minute }

func (r *Reconciler) Work(ctx context.Context, _ *river.Job[ReconcileArgs]) error {
	var accts []dbq.WhatsappAccount
	err := r.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		accts, err = q.ListMetaBillingAccounts(ctx)
		return err
	})
	if err != nil {
		return err
	}
	mismatches, failed := 0, 0
	for _, a := range accts {
		n, err := r.reconcile(ctx, a)
		mismatches += n
		if err != nil {
			// One WABA's failure (an expired token, a Meta error) must not hold up the others.
			failed++
			r.log.Warn("meta billing reconciliation failed", "tenant_id", a.TenantID, "waba_id", a.WabaID, "err", err)
		}
	}
	r.log.Info("meta billing reconciled", "accounts", len(accts), "mismatches", mismatches, "failed", failed)
	return nil
}

type recKey struct {
	day               string
	category, country string
}

func (r *Reconciler) reconcile(ctx context.Context, acct dbq.WhatsappAccount) (int, error) {
	var (
		tenant dbq.Tenant
		token  string
	)
	err := r.db.InTenant(ctx, acct.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if tenant, err = q.GetTenant(ctx, acct.TenantID); err != nil {
			return err
		}
		token, err = credentials.Token(ctx, q, r.keys, acct)
		return err
	})
	if err != nil {
		return 0, err
	}
	loc := analytics.Location(tenant.Timezone)
	now := r.now().In(loc)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc) // today is not finished
	from := to.AddDate(0, 0, -reconcileDays)
	points, err := r.meta.PricingAnalytics(ctx, token, acct.WabaID, from, to)
	if err != nil {
		return 0, err
	}
	currency := "INR"
	if acct.Currency != nil && *acct.Currency != "" {
		currency = *acct.Currency
	}

	type sides struct{ ours, theirs, ourCost, theirCost int64 }
	rows := map[recKey]*sides{}
	get := func(k recKey) *sides {
		if rows[k] == nil {
			rows[k] = &sides{}
		}
		return rows[k]
	}
	for _, p := range points {
		if p.Category == "service" || (p.Volume == 0 && p.Cost == 0) {
			continue // service conversations are free and never in our billable counts
		}
		day := p.Start.In(loc)
		if day.Before(from) || !day.Before(to) {
			continue
		}
		s := get(recKey{day.Format("2006-01-02"), p.Category, p.Country})
		s.theirs += p.Volume
		s.theirCost += int64(math.Round(p.Cost * 100))
	}

	mismatches := 0
	err = r.db.InTenant(ctx, acct.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rates, err := q.ListMetaRates(ctx)
		if err != nil {
			return err
		}
		card := Card(rates)
		usage, err := q.UsageForReconciliation(ctx, dbq.UsageForReconciliationParams{FromDay: date(from), ToDay: date(to.AddDate(0, 0, -1))})
		if err != nil {
			return err
		}
		for _, u := range usage {
			k := recKey{u.Day.Time.Format("2006-01-02"), u.PricingCategory, u.RecipientCountry}
			s := get(k)
			s.ours += u.Billable
			if rate, ok := card.For(k.category, k.country, u.Day.Time); ok {
				s.ourCost += (rate*u.Billable + 50) / 100
			}
		}
		keys := make([]recKey, 0, len(rows))
		for k := range rows {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i], keys[j]
			if a.day != b.day {
				return a.day < b.day
			}
			if a.category != b.category {
				return a.category < b.category
			}
			return a.country < b.country
		})
		for _, k := range keys {
			s := rows[k]
			status := "match"
			if mismatch(s.ours, s.theirs, s.ourCost, s.theirCost, currency) {
				status = "mismatch"
				mismatches++
			}
			day, _ := time.Parse("2006-01-02", k.day)
			if err := q.UpsertReconciliation(ctx, dbq.UpsertReconciliationParams{
				TenantID: acct.TenantID, WabaID: acct.WabaID, Day: date(day), Category: k.category, Country: k.country,
				OurMessages: s.ours, MetaMessages: s.theirs, OurCostMinor: s.ourCost, MetaCostMinor: s.theirCost,
				Currency: currency, Status: status,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return mismatches, err
}

// mismatch says whether our numbers for a day differ from Meta's by more than rounding and the
// timing of late status updates explain. Costs are compared only in rupees, the currency of the rate card.
func mismatch(ours, theirs, ourCost, theirCost int64, currency string) bool {
	if abs(ours-theirs) > max(messageTolerance, theirs/100) {
		return true
	}
	if currency == "INR" && abs(ourCost-theirCost) > max(100, theirCost/50) {
		return true
	}
	return false
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
