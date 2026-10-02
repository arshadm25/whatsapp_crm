package metafees

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

// CreditLineArgs is the job that shares Ecogo's credit line with the WABAs of workspaces that pay
// Meta through Ecogo. It is the step after Embedded Signup, kept as a sweep so it also catches
// workspaces switched to paying through Ecogo later, and does nothing until the Solution Partner
// credit line is configured (ECOGO_META_CREDIT_LINE_ENABLED).
type CreditLineArgs struct{}

func (CreditLineArgs) Kind() string { return "attach_credit_lines" }

func (CreditLineArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueOnboarding, MaxAttempts: 3}
}

// CreditLinePeriodic returns the job that runs every ten minutes.
func CreditLinePeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CreditLineArgs{}, nil },
		&river.PeriodicJobOpts{ID: "attach_credit_lines", RunOnStart: false})
}

type CreditLineWorker struct {
	river.WorkerDefaults[CreditLineArgs]
	db   *db.DB
	meta BillingMeta
	cfg  config.CreditLine
	log  *slog.Logger
}

func NewCreditLineWorker(d *db.DB, meta BillingMeta, cfg config.CreditLine, log *slog.Logger) *CreditLineWorker {
	return &CreditLineWorker{db: d, meta: meta, cfg: cfg, log: log}
}

func (w *CreditLineWorker) Timeout(*river.Job[CreditLineArgs]) time.Duration { return 5 * time.Minute }

func (w *CreditLineWorker) Work(ctx context.Context, _ *river.Job[CreditLineArgs]) error {
	if !w.cfg.Enabled {
		return nil
	}
	var accts []dbq.WhatsappAccount
	err := w.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		accts, err = q.ListCreditLineCandidates(ctx)
		return err
	})
	if err != nil {
		return err
	}
	for _, a := range accts {
		currency := "INR"
		if a.Currency != nil && *a.Currency != "" {
			currency = *a.Currency
		}
		id, attachErr := w.meta.AttachCreditLine(ctx, w.cfg.Token, w.cfg.ID, a.WabaID, currency)
		arg := dbq.SetCreditLineResultParams{ID: a.ID}
		if attachErr != nil {
			// Shown to platform admins; the next sweep tries again.
			msg := attachErr.Error()
			if len(msg) > 300 {
				msg = msg[:300]
			}
			arg.Error = &msg
			w.log.Warn("credit line not attached", "tenant_id", a.TenantID, "waba_id", a.WabaID, "err", attachErr)
		} else {
			arg.AllocationID = &id
		}
		err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			return q.SetCreditLineResult(ctx, arg)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
