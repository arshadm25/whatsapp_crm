package metafees

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

// StatementArgs is the periodic job that issues the statements of finished months.
type StatementArgs struct{}

func (StatementArgs) Kind() string { return "issue_meta_fee_statements" }

func (StatementArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 3}
}

// Periodic returns the hourly job; it is cheap when there is nothing to issue.
func Periodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return StatementArgs{}, nil },
		&river.PeriodicJobOpts{ID: "issue_meta_fee_statements", RunOnStart: true})
}

type Worker struct {
	river.WorkerDefaults[StatementArgs]
	db     *db.DB
	seller config.Seller
	markup int
	log    *slog.Logger
	now    func() time.Time
}

func NewWorker(d *db.DB, seller config.Seller, markupBP int, log *slog.Logger) *Worker {
	return &Worker{db: d, seller: seller, markup: markupBP, log: log, now: time.Now}
}

// At makes the worker read the time from now; tests use it to move past the end of a month.
func (w *Worker) At(now func() time.Time) *Worker {
	w.now = now
	return w
}

func (w *Worker) Timeout(*river.Job[StatementArgs]) time.Duration { return 10 * time.Minute }

func (w *Worker) Work(ctx context.Context, _ *river.Job[StatementArgs]) error {
	var tenants []dbq.ListMetaFeeTenantsRow
	err := w.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		tenants, err = q.ListMetaFeeTenants(ctx)
		return err
	})
	if err != nil {
		return err
	}
	issued, failed := 0, 0
	for _, t := range tenants {
		n, err := w.issueFor(ctx, t)
		issued += n
		if err != nil {
			// One workspace's problem (usually a missing rate) must not hold up the others.
			failed++
			w.log.Error("meta fee statement not issued", "tenant_id", t.ID, "err", err, "needs_rate", errors.Is(err, ErrUnrated))
		}
	}
	w.log.Info("meta fee statements", "tenants", len(tenants), "issued", issued, "failed", failed)
	return nil
}

// issueFor issues every finished month since the workspace started paying through Ecogo.
func (w *Worker) issueFor(ctx context.Context, t dbq.ListMetaFeeTenantsRow) (int, error) {
	loc := analytics.Location(t.Timezone)
	now := w.now()
	since := t.Since.In(loc)
	issued := 0
	for month := MonthStart(since); ; month = month.AddDate(0, 1, 0) {
		// A month is billed once its last days have settled.
		if now.Before(month.AddDate(0, 1, settleDays)) {
			break
		}
		if now.After(month.AddDate(0, maxBackMonths+1, 0)) {
			continue // too old to issue automatically
		}
		from := month
		if since.After(from) {
			from = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)
		}
		var done bool
		err := w.db.InTenant(ctx, t.ID, func(q *dbq.Queries, _ pgx.Tx) error {
			var err error
			done, err = Issue(ctx, q, t.ID, loc, month, from, Params{Seller: w.seller, MarkupBP: w.markup, Now: now})
			return err
		})
		if err != nil {
			return issued, err
		}
		if done {
			issued++
		}
	}
	return issued, nil
}
