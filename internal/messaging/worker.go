package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

// SendArgs is the River job that sends one queued message.
type SendArgs struct {
	MessageID uuid.UUID `json:"message_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
}

func (SendArgs) Kind() string { return "send_message" }

func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       jobs.QueueMessages,
		MaxAttempts: 8,
		UniqueOpts:  river.UniqueOpts{ByArgs: true},
	}
}

// Worker sends queued messages to Meta.
//
// Delivery is at least once: if Meta accepts a message but the response is lost (a timeout),
// the retry sends it again, because the Cloud API has no idempotency key. Throttling and
// Meta-side errors are retried with River's backoff; every other Meta error fails the message.
type Worker struct {
	river.WorkerDefaults[SendArgs]
	db   *db.DB
	keys *envelope.Keyring
	meta Meta
	log  *slog.Logger
}

func NewWorker(d *db.DB, keys *envelope.Keyring, meta Meta, log *slog.Logger) *Worker {
	return &Worker{db: d, keys: keys, meta: meta, log: log}
}

// Timeout leaves room for Meta's own timeout and the database writes around it.
func (w *Worker) Timeout(*river.Job[SendArgs]) time.Duration { return time.Minute }

var errAlreadyHandled = errors.New("message already sent or failed")

func (w *Worker) Work(ctx context.Context, job *river.Job[SendArgs]) error {
	a := job.Args
	ctx = metaclient.WithTenant(ctx, a.TenantID.String())
	log := w.log.With("message_id", a.MessageID, "tenant_id", a.TenantID, "attempt", job.Attempt)

	var (
		row   dbq.GetMessageForSendRow
		token string
	)
	err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if row, err = q.GetMessageForSend(ctx, a.MessageID); err != nil {
			return err
		}
		if row.Message.Status != dbq.MessageStatusQueued || row.Message.Wamid != nil {
			return errAlreadyHandled
		}
		acct, err := q.GetWhatsAppAccount(ctx, row.WhatsappAccountID)
		if err != nil {
			return err
		}
		if acct.Status != dbq.ConnectionStatusConnected || row.PhoneStatus != dbq.ConnectionStatusConnected {
			return errNumberGone
		}
		token, err = credentials.Token(ctx, q, w.keys, acct)
		return err
	})
	switch {
	case errors.Is(err, errAlreadyHandled):
		return nil
	case db.IsNotFound(err):
		log.Warn("send: message gone; dropping job")
		return river.JobCancel(err)
	case errors.Is(err, errNumberGone):
		w.fail(ctx, a, codeNotConnected, nil)
		return river.JobCancel(err)
	case err != nil:
		return err
	}

	var content map[string]any
	if err := json.Unmarshal(row.Message.Content, &content); err != nil {
		w.fail(ctx, a, codeUnreachable, nil)
		return river.JobCancel(err)
	}
	wamid, err := w.meta.SendMessage(ctx, token, row.MetaPhoneNumberID, row.ContactWaID, content)
	if err == nil {
		return w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			_, err := q.MarkMessageSent(ctx, dbq.MarkMessageSentParams{ID: a.MessageID, Wamid: &wamid})
			return err
		})
	}

	final := job.Attempt >= job.MaxAttempts
	var me *metaclient.Error
	if errors.As(err, &me) {
		if (me.Retryable() || me.Code == 131056) && !final {
			log.Warn("send: meta asked us to retry", "code", me.Code)
			return err
		}
		title := me.Friendly()
		w.fail(ctx, a, int32(me.Code), &title)
		return river.JobCancel(err)
	}
	if !final {
		log.Warn("send failed, will retry", "err", err)
		return err
	}
	w.fail(ctx, a, codeUnreachable, nil)
	return river.JobCancel(err)
}

var errNumberGone = errors.New("number not connected")

// fail marks a still-queued message as failed and records the status event.
func (w *Worker) fail(ctx context.Context, a SendArgs, code int32, title *string) {
	ctx = context.WithoutCancel(ctx)
	if title == nil {
		if m, ok := ownErrors[code]; ok {
			title = &m.message
		}
	}
	err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		n, err := q.MarkMessageFailed(ctx, dbq.MarkMessageFailedParams{ID: a.MessageID, ErrorCode: &code, ErrorTitle: title})
		if err != nil || n == 0 {
			return err
		}
		return q.InsertMessageStatusEvent(ctx, dbq.InsertMessageStatusEventParams{
			TenantID: a.TenantID, MessageID: a.MessageID, Status: dbq.MessageStatusFailed,
			ErrorCode: &code, ErrorTitle: title, OccurredAt: time.Now().UTC(),
		})
	})
	if err != nil {
		w.log.Error("send: record failure", "message_id", a.MessageID, "err", err)
	}
}
