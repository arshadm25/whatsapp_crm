package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

// receiver is a client's webhook endpoint.
type receiver struct {
	mu     sync.Mutex
	status int
	got    []received
}

type received struct {
	header http.Header
	body   []byte
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.got = append(rc.got, received{r.Header.Clone(), b})
	w.WriteHeader(rc.status)
}

// deliverAll works every queued delivery job once, the way River would on attempt n.
func (h *harness) deliverAll(tenantID uuid.UUID, w *webhooks.Worker, attempt int) []error {
	h.t.Helper()
	var args []webhooks.DeliverArgs
	err := h.db.InTenant(context.Background(), tenantID, func(_ *dbq.Queries, tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), "SELECT args FROM river_job WHERE kind = 'deliver_webhook' AND state = 'available' ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var a webhooks.DeliverArgs
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			_ = json.Unmarshal(raw, &a)
			args = append(args, a)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Mark them taken so the next call only sees new jobs.
		_, err = tx.Exec(context.Background(), "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE kind = 'deliver_webhook' AND state = 'available'")
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	var errs []error
	for _, a := range args {
		errs = append(errs, w.Work(context.Background(), &river.Job[webhooks.DeliverArgs]{
			JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: webhooks.MaxAttempts}, Args: a,
		}))
	}
	return errs
}

func TestClientWebhooks(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	rcv := &receiver{status: http.StatusOK}
	srv := httptest.NewTLSServer(rcv)
	t.Cleanup(srv.Close)
	worker := webhooks.NewWorker(h.db, h.keys, srv.Client(), h.log)

	c.do("POST", "/v1/webhook-endpoints", map[string]any{"url": "http://example.com/hook", "event_types": []string{"message.received"}}, http.StatusBadRequest, nil)
	c.do("POST", "/v1/webhook-endpoints", map[string]any{"url": srv.URL, "event_types": []string{"message.sent"}}, http.StatusBadRequest, nil)
	var ep webhooks.CreatedEndpoint
	c.do("POST", "/v1/webhook-endpoints", map[string]any{
		"url": srv.URL + "/ecogo", "description": "Order system", "event_types": []string{"message.received", "message.status"},
	}, http.StatusCreated, &ep)
	if !strings.HasPrefix(ep.Secret, "whsec_") || !ep.Enabled || len(ep.EventTypes) != 2 {
		t.Fatalf("endpoint = %+v", ep)
	}
	var list struct{ Data []json.RawMessage }
	c.do("GET", "/v1/webhook-endpoints", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || strings.Contains(string(list.Data[0]), ep.Secret) {
		t.Fatalf("list = %s", list.Data)
	}

	// A customer message becomes a signed message.received delivery.
	h.inbound(customer, "wamid.IN1", "Where is my order?")
	if errs := h.deliverAll(me.Tenant.ID, worker, 1); len(errs) != 1 || errs[0] != nil {
		t.Fatalf("deliveries: %v", errs)
	}
	got := rcv.got[0]
	var env struct {
		ID       uuid.UUID
		Type     string
		TenantID uuid.UUID `json:"tenant_id"`
		Data     messaging.Message
	}
	if err := json.Unmarshal(got.body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "message.received" || env.TenantID != me.Tenant.ID || env.Data.Contact.WaID != customer ||
		got.header.Get("Ecogo-Event-Id") != env.ID.String() {
		t.Fatalf("event = %+v %v", env, got.header)
	}
	sig := got.header.Get("Ecogo-Signature")
	ts, _, _ := strings.Cut(strings.TrimPrefix(sig, "t="), ",")
	var unix int64
	_ = json.Unmarshal([]byte(ts), &unix)
	if want := webhooks.Sign([]byte(ep.Secret), time.Unix(unix, 0), got.body); sig != want {
		t.Fatalf("signature %q, want %q", sig, want)
	}

	// A send that reaches Meta becomes message.status; the endpoint failing is retried.
	rcv.status = http.StatusInternalServerError
	var msg messaging.Message
	c.send(text(phone.ID, "It ships today"), "", http.StatusAccepted, &msg)
	if err := h.runSend(me.Tenant.ID, msg.ID, 1); err != nil {
		t.Fatal(err)
	}
	if errs := h.deliverAll(me.Tenant.ID, worker, 1); len(errs) != 1 || errs[0] == nil {
		t.Fatalf("failed delivery returned %v", errs)
	}
	var page struct {
		Data []webhooks.Delivery
	}
	c.do("GET", "/v1/webhook-endpoints/"+ep.ID.String()+"/deliveries", nil, http.StatusOK, &page)
	if len(page.Data) != 2 || page.Data[0].EventType != "message.status" || page.Data[0].Status != "retrying" ||
		page.Data[0].AttemptCount != 1 || page.Data[0].LastResponseCode == nil || *page.Data[0].LastResponseCode != 500 || page.Data[0].NextAttemptAt == nil ||
		page.Data[1].Status != "succeeded" {
		t.Fatalf("deliveries = %+v", page.Data)
	}

	// Retrying now delivers it once the endpoint is back.
	rcv.status = http.StatusNoContent
	c.do("POST", "/v1/webhook-endpoints/"+ep.ID.String()+"/deliveries/"+page.Data[0].ID.String()+"/retry", nil, http.StatusAccepted, nil)
	if errs := h.deliverAll(me.Tenant.ID, worker, 1); len(errs) != 1 || errs[0] != nil {
		t.Fatalf("retry: %v", errs)
	}
	c.do("GET", "/v1/webhook-endpoints/"+ep.ID.String()+"/deliveries?status=succeeded", nil, http.StatusOK, &page)
	if len(page.Data) != 2 {
		t.Fatalf("succeeded deliveries = %+v", page.Data)
	}

	// The last attempt marks a delivery dead.
	rcv.status = http.StatusBadGateway
	if err := h.status("wamid.TEST1", "delivered"); err != nil {
		t.Fatal(err)
	}
	if errs := h.deliverAll(me.Tenant.ID, worker, webhooks.MaxAttempts); len(errs) != 1 || errs[0] == nil {
		t.Fatalf("final attempt: %v", errs)
	}
	c.do("GET", "/v1/webhook-endpoints/"+ep.ID.String()+"/deliveries?status=dead", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].NextAttemptAt != nil {
		t.Fatalf("dead deliveries = %+v", page.Data)
	}

	// The default client never calls private or loopback addresses.
	rcv.status = http.StatusOK
	h.inbound(customer, "wamid.IN2", "Thanks")
	if errs := h.deliverAll(me.Tenant.ID, webhooks.NewWorker(h.db, h.keys, nil, h.log), 1); len(errs) != 1 || errs[0] == nil ||
		!strings.Contains(errs[0].Error(), "refusing") {
		t.Fatalf("loopback delivery: %v", errs)
	}

	// Other workspaces cannot see or delete the endpoint; deleting stops deliveries.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", "/v1/webhook-endpoints/"+ep.ID.String()+"/deliveries", nil, http.StatusNotFound, nil)
	other.do("DELETE", "/v1/webhook-endpoints/"+ep.ID.String(), nil, http.StatusNotFound, nil)
	c.do("DELETE", "/v1/webhook-endpoints/"+ep.ID.String(), nil, http.StatusNoContent, nil)
	n := len(rcv.got)
	h.inbound(customer, "wamid.IN3", "Bye")
	h.deliverAll(me.Tenant.ID, worker, 1)
	if len(rcv.got) != n {
		t.Fatal("deleted endpoint still received events")
	}
}
