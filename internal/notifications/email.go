package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
)

// EmailArgs is the periodic job that emails the notifications members chose to get by email.
type EmailArgs struct{}

func (EmailArgs) Kind() string { return "send_notification_emails" }

func (EmailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 1}
}

// Periodic returns the notification email job for the worker's River client.
func Periodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return EmailArgs{}, nil },
		&river.PeriodicJobOpts{ID: "send_notification_emails", RunOnStart: true})
}

// emailBatch bounds the emails sent per workspace in one run.
const emailBatch = 200

type EmailWorker struct {
	river.WorkerDefaults[EmailArgs]
	db     *db.DB
	mail   mailer.Mailer
	appURL string
	log    *slog.Logger
}

func NewEmailWorker(d *db.DB, m mailer.Mailer, appURL string, log *slog.Logger) *EmailWorker {
	return &EmailWorker{db: d, mail: m, appURL: appURL, log: log}
}

func (w *EmailWorker) Timeout(*river.Job[EmailArgs]) time.Duration { return 5 * time.Minute }

func (w *EmailWorker) Work(ctx context.Context, _ *river.Job[EmailArgs]) error {
	var tenants []uuid.UUID
	err := w.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		tenants, err = q.NotificationEmailTenants(ctx)
		return err
	})
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := w.send(ctx, t); err != nil {
			w.log.Error("notifications: emails failed", "tenant_id", t, "err", err)
		}
	}
	return nil
}

// send emails one workspace's pending notifications. A notification whose email fails is marked
// failed rather than retried, so a bad address cannot block the rest.
func (w *EmailWorker) send(ctx context.Context, tenantID uuid.UUID) error {
	var rows []dbq.ClaimNotificationEmailsRow
	// Claim first and send after the commit, so a slow mail relay holds no locks.
	err := w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if rows, err = q.ClaimNotificationEmails(ctx, emailBatch); err != nil {
			return err
		}
		for _, n := range rows {
			if err := q.SetNotificationEmail(ctx, dbq.SetNotificationEmailParams{ID: n.ID, Email: "sent"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, n := range rows {
		if err := w.mail.Send(ctx, w.message(n)); err != nil {
			w.log.Warn("notifications: email not sent", "notification_id", n.ID, "err", err)
			err = w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
				return q.SetNotificationEmail(ctx, dbq.SetNotificationEmailParams{ID: n.ID, Email: "failed"})
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *EmailWorker) message(n dbq.ClaimNotificationEmailsRow) mailer.Message {
	link := w.appURL + "/"
	if n.Link != nil {
		link = w.appURL + *n.Link
	}
	body := n.Body
	if body != "" {
		body += "\n\n"
	}
	return mailer.Message{
		To:      n.UserEmail,
		Subject: n.Title,
		Text: fmt.Sprintf("Hi %s,\n\n%s.\n\n%sOpen Ecogo WhatsApp: %s\n\n"+
			"You can change which emails you get under Settings, Notifications.\n\nEcogo Software Solutions Pvt Ltd\n",
			n.UserName, n.Title, body, link),
	}
}
