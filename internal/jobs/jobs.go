// Package jobs sets up River, the Postgres-backed job queue. Jobs are inserted in the same
// transaction as the rows they act on, so a committed change always has its job.
package jobs

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

// Queue names. Onboarding, Meta webhooks, outbound messages and client webhook deliveries
// have their own queues, so a backlog in one never delays the others.
const (
	QueueDefault    = river.QueueDefault
	QueueOnboarding = "onboarding"
	QueueMetaEvents = "meta_events"
	QueueMessages   = "messages"
	QueueWebhooks   = "webhooks"
)

// Inserter is the part of the River client that request handlers need.
type Inserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// NewInsertOnly returns a client that can enqueue jobs but never works them (api, ingest).
func NewInsertOnly(pool *pgxpool.Pool, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log})
}

// NewWorkerClient returns a client that works the given workers (worker deployment).
func NewWorkerClient(pool *pgxpool.Pool, workers *river.Workers, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:  log,
		Workers: workers,
		Queues:  Queues,
	})
}

// Queues is every queue the worker deployment works. A job inserted into a queue missing here
// is never picked up.
var Queues = map[string]river.QueueConfig{
	QueueDefault:    {MaxWorkers: 50},
	QueueOnboarding: {MaxWorkers: 10},
	QueueMetaEvents: {MaxWorkers: 50},
	QueueMessages:   {MaxWorkers: 50},
	QueueWebhooks:   {MaxWorkers: 50},
}

// From returns ins, or when it is nil the River client working the current job. Workers use it
// to enqueue follow-up jobs in their own transaction.
func From(ctx context.Context, ins Inserter) (Inserter, error) {
	if ins != nil {
		return ins, nil
	}
	return river.ClientFromContextSafely[pgx.Tx](ctx)
}
