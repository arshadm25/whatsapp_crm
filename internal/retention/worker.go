// Package retention enforces each workspace's message retention setting: a daily job deletes
// messages (and the media files they carried) older than the workspace's chosen number of days.
package retention

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/storage"
)

// PurgeArgs is the periodic job that applies every workspace's retention setting.
type PurgeArgs struct{}

func (PurgeArgs) Kind() string { return "purge_expired_data" }

func (PurgeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 3}
}

// Periodic returns the daily purge job for the worker's River client.
func Periodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return PurgeArgs{}, nil },
		&river.PeriodicJobOpts{ID: "purge_expired_data", RunOnStart: false})
}

// batch bounds one delete statement, so a workspace with a large backlog never holds a long lock.
const batch = 2000

type Worker struct {
	river.WorkerDefaults[PurgeArgs]
	db    *db.DB
	store storage.Store
	log   *slog.Logger
	now   func() time.Time
}

func NewWorker(d *db.DB, store storage.Store, log *slog.Logger) *Worker {
	return &Worker{db: d, store: store, log: log, now: time.Now}
}

func (w *Worker) Timeout(*river.Job[PurgeArgs]) time.Duration { return 2 * time.Hour }

func (w *Worker) Work(ctx context.Context, _ *river.Job[PurgeArgs]) error {
	var tenants []dbq.ListRetentionTenantsRow
	err := w.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		tenants, err = q.ListRetentionTenants(ctx)
		return err
	})
	if err != nil {
		return err
	}
	failed := 0
	for _, t := range tenants {
		if t.MessageRetentionDays == nil {
			continue
		}
		cutoff := w.now().AddDate(0, 0, -int(*t.MessageRetentionDays))
		if err := w.purge(ctx, t.ID, cutoff); err != nil {
			// One workspace's failure should not hold up the others; tomorrow's run retries it.
			failed++
			w.log.Error("retention: purge failed", "tenant_id", t.ID, "err", err)
		}
	}
	if failed > 0 {
		return &partialError{failed: failed}
	}
	return nil
}

type partialError struct{ failed int }

func (e *partialError) Error() string { return "retention: some workspaces failed" }

// purge removes everything in one workspace that is older than cutoff.
func (w *Worker) purge(ctx context.Context, tenantID uuid.UUID, cutoff time.Time) error {
	var messages, files int64
	for {
		var n int64
		err := w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			var err error
			n, err = q.PurgeOldMessages(ctx, dbq.PurgeOldMessagesParams{Cutoff: cutoff, Batch: batch})
			return err
		})
		if err != nil {
			return err
		}
		messages += n
		if n < batch {
			break
		}
	}
	for {
		var expired []dbq.ListExpiredMediaRow
		err := w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			var err error
			expired, err = q.ListExpiredMedia(ctx, dbq.ListExpiredMediaParams{Cutoff: cutoff, Batch: batch})
			return err
		})
		if err != nil {
			return err
		}
		for _, m := range expired {
			// The file goes first: a failed row delete leaves a row pointing at nothing, which
			// the next run removes, whereas the reverse would orphan the file for good.
			if err := w.store.Delete(ctx, m.StorageKey); err != nil {
				return err
			}
			if err := w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error { return q.DeleteMedia(ctx, m.ID) }); err != nil {
				return err
			}
			files++
		}
		if len(expired) < batch {
			break
		}
	}
	var previews int64
	err := w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if previews, err = q.ClearOldConversationPreviews(ctx, &cutoff); err != nil {
			return err
		}
		if messages == 0 && files == 0 {
			return nil
		}
		md, _ := json.Marshal(map[string]any{"messages": messages, "media_files": files, "before": cutoff})
		return q.InsertAuditLog(ctx, dbq.InsertAuditLogParams{
			TenantID: &tenantID, ActorType: dbq.ActorTypeSystem, Action: "retention.purge", Metadata: md,
		})
	})
	if messages > 0 || files > 0 || previews > 0 {
		w.log.Info("retention: purged", "tenant_id", tenantID, "messages", messages, "media_files", files)
	}
	return err
}
