package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/retention"
	"github.com/arshadm25/whatsapp_crm/internal/storage"
)

func TestMessageRetention(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	ctx := context.Background()

	// The setting needs 30 days or more, and can be cleared.
	ws := map[string]any{"name": "Sharma Sweets", "time_zone": "Asia/Kolkata"}
	ws["message_retention_days"] = 7
	c.do("PATCH", "/internal/team/workspace", ws, http.StatusBadRequest, nil)
	ws["message_retention_days"] = 90
	var got struct {
		Days *int32 `json:"message_retention_days"`
	}
	c.do("PATCH", "/internal/team/workspace", ws, http.StatusOK, &got)
	if got.Days == nil || *got.Days != 90 {
		t.Fatalf("workspace = %+v", got)
	}

	h.inbound(customer, "wamid.OLD", "an old message")
	h.inbound(customer, "wamid.NEW", "a recent message")
	store, err := storage.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "old/file.jpg", strings.NewReader("jpeg"), 4, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -120)
	err = h.db.InTenant(ctx, me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "UPDATE messages SET created_at = $1 WHERE wamid = 'wamid.OLD'", old); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE conversations SET last_message_at = $1", old); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO media (tenant_id, storage_key, mime_type, size_bytes, created_at)
			VALUES ($1, 'old/file.jpg', 'image/jpeg', 4, $2)`, me.Tenant.ID, old)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	w := retention.NewWorker(h.db, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := w.Work(ctx, &river.Job[retention.PurgeArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3}}); err != nil {
		t.Fatal(err)
	}

	err = h.db.InTenant(ctx, me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		var oldLeft, newLeft, mediaLeft, audits int
		var preview *string
		for sql, dst := range map[string]*int{
			"SELECT count(*) FROM messages WHERE wamid = 'wamid.OLD'":         &oldLeft,
			"SELECT count(*) FROM messages WHERE wamid = 'wamid.NEW'":         &newLeft,
			"SELECT count(*) FROM media":                                      &mediaLeft,
			"SELECT count(*) FROM audit_log WHERE action = 'retention.purge'": &audits,
		} {
			if err := tx.QueryRow(ctx, sql).Scan(dst); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, "SELECT last_message_preview FROM conversations").Scan(&preview); err != nil {
			return err
		}
		if oldLeft != 0 || newLeft != 1 || mediaLeft != 0 || audits != 1 || preview != nil {
			t.Errorf("old %d new %d media %d audits %d preview %v", oldLeft, newLeft, mediaLeft, audits, preview)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "old/file.jpg"); err == nil {
		t.Error("expired file still stored")
	}

	// With no setting, nothing is deleted.
	ws["message_retention_days"] = nil
	c.do("PATCH", "/internal/team/workspace", ws, http.StatusOK, nil)
}
