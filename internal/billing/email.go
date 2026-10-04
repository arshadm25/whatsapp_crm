package billing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
)

// EmailArgs is the River job that emails a new invoice to the workspace's owners. It is queued
// in the transaction that issues the invoice, so an issued invoice is always emailed.
type EmailArgs struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
}

func (EmailArgs) Kind() string { return "send_invoice_email" }

func (EmailArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 8} }

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

func (w *EmailWorker) Work(ctx context.Context, job *river.Job[EmailArgs]) error {
	a := job.Args
	var (
		inv    dbq.Invoice
		owners []string
	)
	err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if inv, err = q.GetInvoice(ctx, a.InvoiceID); err != nil {
			return err
		}
		if inv.EmailedAt != nil {
			return nil
		}
		owners, err = q.OwnerEmails(ctx)
		return err
	})
	if db.IsNotFound(err) {
		return river.JobCancel(err) // the workspace or invoice is gone
	}
	if err != nil {
		return err
	}
	if inv.EmailedAt != nil || len(owners) == 0 {
		return nil
	}
	subject := fmt.Sprintf("Invoice %s from Ecogo: ₹%s", inv.Number, inr(inv.TotalMinor))
	body := fmt.Sprintf("Hello,\n\nThank you for your payment. Your GST invoice is ready.\n\n"+
		"Invoice: %s\nFor: %s\nAmount: ₹%s (GST included)\n\n"+
		"View, print or save it as a PDF (log in first): %s/internal/billing/invoices/%s/view\n"+
		"All your invoices are under Settings, Billing.\n\nEcogo AI Technologies Pvt Ltd\n",
		inv.Number, inv.Description, inr(inv.TotalMinor), w.appURL, inv.ID)
	for _, to := range owners {
		if err := w.mail.Send(ctx, mailer.Message{To: to, Subject: subject, Text: body}); err != nil {
			return fmt.Errorf("email invoice %s to %s: %w", inv.Number, to, err)
		}
	}
	return w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.MarkInvoiceEmailed(ctx, inv.ID)
	})
}
