package server_test

import (
	"testing"

	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/campaigns"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/metaevents"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
)

// Every job kind must go to a queue the worker deployment works, or it waits forever.
func TestJobQueuesAreWorked(t *testing.T) {
	type jobArgs interface {
		river.JobArgs
		river.JobArgsWithInsertOpts
	}
	for _, args := range []jobArgs{onboarding.Args{}, metaevents.ProcessArgs{}, messaging.SendArgs{}, campaigns.RunArgs{}, analytics.RollupArgs{}} {
		q := args.InsertOpts().Queue
		if _, ok := jobs.Queues[q]; !ok {
			t.Errorf("%s jobs go to queue %q, which the worker does not work", args.Kind(), q)
		}
	}
}
