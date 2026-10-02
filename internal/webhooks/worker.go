package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

// DeliverArgs is the River job that posts one delivery.
type DeliverArgs struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	DeliveryID uuid.UUID `json:"delivery_id"`
}

func (DeliverArgs) Kind() string { return "deliver_webhook" }

// MaxAttempts with River's backoff (attempt⁴ seconds) spreads retries over about 24 hours.
const MaxAttempts = 13

func (DeliverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueWebhooks, MaxAttempts: MaxAttempts}
}

// SecretAAD binds an endpoint's signing secret to its tenant and endpoint.
func SecretAAD(tenantID, endpointID uuid.UUID) []byte {
	return []byte("webhook_endpoints:" + tenantID.String() + ":" + endpointID.String())
}

// Sign returns the Ecogo-Signature header value for body at time t.
func Sign(secret []byte, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts + "."))
	m.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// Worker works DeliverArgs.
type Worker struct {
	river.WorkerDefaults[DeliverArgs]
	db   *db.DB
	keys *envelope.Keyring
	http *http.Client
	log  *slog.Logger
	now  func() time.Time
}

// NewWorker posts deliveries with client; nil means the default client, which refuses
// private and loopback addresses.
func NewWorker(d *db.DB, keys *envelope.Keyring, client *http.Client, log *slog.Logger) *Worker {
	if client == nil {
		client = PublicClient()
	}
	return &Worker{db: d, keys: keys, http: client, log: log, now: time.Now}
}

// PublicClient is an HTTP client that only connects to public addresses, does not follow
// redirects and gives up after 10 seconds.
func PublicClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return fmt.Errorf("webhook: refusing to connect to %s", host)
		}
		return nil
	}}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = dialer.DialContext
	tr.Proxy = nil
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (w *Worker) Timeout(*river.Job[DeliverArgs]) time.Duration { return 30 * time.Second }

var errSkip = errors.New("delivery needs no attempt")

func (w *Worker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	a := job.Args
	var row dbq.GetDeliveryForSendRow
	err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		row, err = q.GetDeliveryForSend(ctx, a.DeliveryID)
		if err == nil && (row.WebhookDelivery.Status == dbq.DeliveryStatusSucceeded || !row.WebhookEndpoint.IsEnabled) {
			return errSkip
		}
		return err
	})
	switch {
	case errors.Is(err, errSkip):
		return nil
	case db.IsNotFound(err):
		return river.JobCancel(err) // the endpoint was deleted
	case err != nil:
		return err
	}
	secret, err := w.keys.OpenCompact(row.WebhookEndpoint.SecretCiphertext, SecretAAD(a.TenantID, row.WebhookEndpoint.ID))
	if err != nil {
		return river.JobCancel(err)
	}

	code, sendErr := w.post(ctx, row.WebhookEndpoint.Url, secret, row.WebhookDelivery)
	status, next := dbq.DeliveryStatusSucceeded, w.now()
	var errText *string
	if sendErr != nil {
		s := sendErr.Error()
		if len(s) > 500 {
			s = s[:500]
		}
		errText = &s
		status = dbq.DeliveryStatusRetrying
		next = w.now().Add(time.Duration(job.Attempt*job.Attempt*job.Attempt*job.Attempt) * time.Second)
		if job.Attempt >= job.MaxAttempts {
			status = dbq.DeliveryStatusDead
		}
	}
	err = w.db.InTenant(context.WithoutCancel(ctx), a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.RecordDeliveryAttempt(ctx, dbq.RecordDeliveryAttemptParams{
			ID: a.DeliveryID, Status: status, LastResponseCode: code, LastError: errText, NextAttemptAt: next,
		})
	})
	if err != nil {
		return err
	}
	if sendErr != nil {
		if status == dbq.DeliveryStatusDead {
			return river.JobCancel(sendErr)
		}
		return sendErr
	}
	return nil
}

// post sends the delivery and returns the response status; any non-2xx answer is an error.
func (w *Worker) post(ctx context.Context, url string, secret []byte, d dbq.WebhookDelivery) (*int32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(d.Payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Ecogo-Webhooks/1.0")
	req.Header.Set("Ecogo-Event-Id", d.EventID.String())
	req.Header.Set("Ecogo-Event-Type", d.EventType)
	req.Header.Set("Ecogo-Signature", Sign(secret, w.now(), d.Payload))
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	code := int32(resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &code, fmt.Errorf("endpoint answered %d", resp.StatusCode)
	}
	return &code, nil
}

func unwrapURL(err error) error {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) {
		if inner := ue.Unwrap(); inner != nil {
			return inner
		}
	}
	return err
}
