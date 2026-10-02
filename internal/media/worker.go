package media

import (
	"context"
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
	"github.com/arshadm25/whatsapp_crm/internal/storage"
)

// DownloadArgs is the River job that copies an inbound file from Meta. Meta's download links
// expire, so the copy is taken on arrival.
type DownloadArgs struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	MessageID   uuid.UUID `json:"message_id"`
	MetaMediaID string    `json:"meta_media_id"`
	Filename    string    `json:"filename,omitempty"`
}

func (DownloadArgs) Kind() string { return "download_media" }

func (DownloadArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMessages, MaxAttempts: 10, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// DownloadWorker works DownloadArgs.
type DownloadWorker struct {
	river.WorkerDefaults[DownloadArgs]
	db    *db.DB
	keys  *envelope.Keyring
	meta  Meta
	store storage.Store
	log   *slog.Logger
}

func NewDownloadWorker(d *db.DB, keys *envelope.Keyring, meta Meta, store storage.Store, log *slog.Logger) *DownloadWorker {
	return &DownloadWorker{db: d, keys: keys, meta: meta, store: store, log: log}
}

func (w *DownloadWorker) Timeout(*river.Job[DownloadArgs]) time.Duration { return 10 * time.Minute }

var errHasMedia = errors.New("message already has its media")

func (w *DownloadWorker) Work(ctx context.Context, job *river.Job[DownloadArgs]) error {
	a := job.Args
	ctx = metaclient.WithTenant(ctx, a.TenantID.String())
	var (
		row   dbq.GetMessageForMediaDownloadRow
		token string
	)
	err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if row, err = q.GetMessageForMediaDownload(ctx, a.MessageID); err != nil {
			return err
		}
		if row.Message.MediaID != nil {
			return errHasMedia
		}
		acct, err := q.GetWhatsAppAccount(ctx, row.WhatsappAccountID)
		if err != nil {
			return err
		}
		token, err = credentials.Token(ctx, q, w.keys, acct)
		return err
	})
	switch {
	case errors.Is(err, errHasMedia):
		return nil
	case db.IsNotFound(err):
		return river.JobCancel(err)
	case err != nil:
		return err
	}

	info, err := w.meta.GetMedia(ctx, token, a.MetaMediaID)
	if err != nil {
		return w.metaErr(err)
	}
	if info.FileSize > MaxSize {
		return river.JobCancel(errTooLarge)
	}
	body, err := w.meta.DownloadMedia(ctx, token, info.URL)
	if err != nil {
		return w.metaErr(err)
	}
	file, err := spool(body, MaxSize)
	body.Close()
	if errors.Is(err, errTooLarge) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if file.size == 0 {
		return river.JobCancel(errors.New("media download: empty file"))
	}

	mimeType := NormalizeType(info.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	id := db.NewID()
	key := Key(a.TenantID, id)
	if err := w.store.Put(ctx, key, file, file.size, mimeType); err != nil {
		return err
	}
	return w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.InsertMedia(ctx, dbq.InsertMediaParams{
			ID: id, TenantID: a.TenantID, PhoneNumberID: &row.Message.PhoneNumberID, StorageKey: key, MimeType: mimeType,
			SizeBytes: file.size, Sha256: file.sha256, Filename: nonEmpty(a.Filename), MetaMediaID: &a.MetaMediaID,
		})
		if err != nil {
			return err
		}
		return q.SetMessageMedia(ctx, dbq.SetMessageMediaParams{ID: a.MessageID, MediaID: &id})
	})
}

// metaErr retries throttling and Meta-side failures and gives up on the rest (an unknown or
// expired media ID).
func (w *DownloadWorker) metaErr(err error) error {
	var me *metaclient.Error
	if errors.As(err, &me) && !me.Retryable() {
		w.log.Warn("media download: meta refused", "code", me.Code, "status", me.HTTPStatus)
		return river.JobCancel(err)
	}
	return err
}
