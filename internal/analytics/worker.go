package analytics

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

// RollupArgs is the periodic job that refreshes the last few days of usage for every tenant.
type RollupArgs struct{}

func (RollupArgs) Kind() string { return "rollup_usage" }

func (RollupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 3}
}

// rollupDays is how far back statuses and pricing are still expected to change a day's numbers.
const rollupDays = 3

// Periodic returns the hourly rollup job for the worker's River client.
func Periodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return RollupArgs{}, nil },
		&river.PeriodicJobOpts{ID: "rollup_usage", RunOnStart: true})
}

type Worker struct {
	river.WorkerDefaults[RollupArgs]
	db  *db.DB
	log *slog.Logger
	now func() time.Time
}

func NewWorker(d *db.DB, log *slog.Logger) *Worker { return &Worker{db: d, log: log, now: time.Now} }

func (w *Worker) Timeout(*river.Job[RollupArgs]) time.Duration { return 10 * time.Minute }

func (w *Worker) Work(ctx context.Context, _ *river.Job[RollupArgs]) error {
	var tenants []dbq.ListActiveTenantsRow
	err := w.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		tenants, err = q.ListActiveTenants(ctx)
		return err
	})
	if err != nil {
		return err
	}
	failed := 0
	for _, t := range tenants {
		loc := Location(t.Timezone)
		today := w.now().In(loc)
		err := w.db.InTenant(ctx, t.ID, func(q *dbq.Queries, _ pgx.Tx) error {
			for i := range rollupDays {
				if err := Rollup(ctx, q, t.ID, loc, today.AddDate(0, 0, -i)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			// One tenant's failure should not hold up the others; the next hour retries it.
			failed++
			w.log.Error("usage rollup failed", "tenant_id", t.ID, "err", err)
		}
	}
	w.log.Info("usage rolled up", "tenants", len(tenants), "failed", failed)
	return nil
}
