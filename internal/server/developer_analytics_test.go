package server_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/devportal"
	"github.com/arshadm25/whatsapp_crm/internal/retention"
	"github.com/arshadm25/whatsapp_crm/internal/storage"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

func TestDeveloperAnalyticsAndRequestLog(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	ctx := context.Background()

	var shop, crm devportal.CreatedKey
	c.do("POST", "/internal/developers/api-keys", map[string]any{"name": "Shop"}, http.StatusCreated, &shop)
	c.do("POST", "/internal/developers/api-keys", map[string]any{"name": "CRM"}, http.StatusCreated, &crm)
	h.bearer("Bearer "+shop.Key, "GET", "/v1/phone-numbers", nil)
	h.bearer("Bearer "+shop.Key, "GET", "/v1/templates", nil)
	h.bearer("Bearer "+shop.Key, "GET", "/v1/conversations/not-an-id", nil)
	h.bearer("Bearer "+crm.Key, "GET", "/v1/phone-numbers", nil)

	// Every API call is in the request log, newest first, with its route and status.
	var logs struct {
		Data       []devportal.LogEntry `json:"data"`
		NextBefore *int64               `json:"next_before"`
	}
	c.do("GET", "/internal/developers/logs", nil, http.StatusOK, &logs)
	if len(logs.Data) != 4 || logs.Data[0].KeyName != "CRM" || logs.Data[0].Route != "/v1/phone-numbers" || logs.Data[0].Status != 200 || logs.NextBefore != nil {
		t.Fatalf("logs = %+v", logs)
	}
	c.do("GET", "/internal/developers/logs?status=errors", nil, http.StatusOK, &logs)
	if len(logs.Data) != 1 || logs.Data[0].Status != 404 || logs.Data[0].RequestID == "" || logs.Data[0].Path != "/v1/conversations/not-an-id" || logs.Data[0].Route != "/v1/conversations/{id}" {
		t.Fatalf("error logs = %+v", logs)
	}
	c.do("GET", "/internal/developers/logs?api_key_id="+shop.ID.String()+"&status=2xx&path=templ", nil, http.StatusOK, &logs)
	if len(logs.Data) != 1 || logs.Data[0].Path != "/v1/templates" {
		t.Fatalf("filtered logs = %+v", logs)
	}
	c.do("GET", "/internal/developers/logs?limit=2", nil, http.StatusOK, &logs)
	if len(logs.Data) != 2 || logs.NextBefore == nil {
		t.Fatalf("page 1 = %+v", logs)
	}
	c.do("GET", fmt.Sprintf("/internal/developers/logs?limit=2&before=%d", *logs.NextBefore), nil, http.StatusOK, &logs)
	if len(logs.Data) != 2 || logs.Data[1].Path != "/v1/phone-numbers" {
		t.Fatalf("page 2 = %+v", logs)
	}
	c.do("GET", "/internal/developers/logs?status=teapot", nil, http.StatusBadRequest, nil)

	var sum devportal.LogSummary
	c.do("GET", "/internal/developers/logs/summary?days=1", nil, http.StatusOK, &sum)
	if sum.Calls != 4 || sum.Errors != 1 || !sum.Hourly || len(sum.Points) != 1 || len(sum.ByStatus) != 2 || sum.ByStatus[0].Status != 200 || sum.ByStatus[0].N != 3 {
		t.Fatalf("summary = %+v", sum)
	}
	if len(sum.ByRoute) != 3 || sum.ByRoute[0].Route != "/v1/phone-numbers" || sum.ByRoute[0].Calls != 2 {
		t.Fatalf("routes = %+v", sum.ByRoute)
	}
	c.do("GET", "/internal/developers/logs/summary?days=31", nil, http.StatusBadRequest, nil)

	// The API keys page: calls per key and day.
	var usage devportal.KeyUsageReport
	c.do("GET", "/internal/developers/usage?days=7", nil, http.StatusOK, &usage)
	if usage.Calls != 4 || usage.Errors != 1 || usage.Hourly || usage.Days != 7 || len(usage.ByKey) != 2 || usage.ByKey[0].APIKeyID != shop.ID || len(usage.Points) != 2 {
		t.Fatalf("usage = %+v", usage)
	}

	// The Webhooks page: deliveries by outcome, per endpoint.
	rcv := &receiver{status: http.StatusOK}
	srv := httptest.NewTLSServer(rcv)
	t.Cleanup(srv.Close)
	worker := webhooks.NewWorker(h.db, h.keys, srv.Client(), h.log)
	var ep webhooks.CreatedEndpoint
	c.do("POST", "/v1/webhook-endpoints", map[string]any{"url": srv.URL, "event_types": []string{"message.received"}}, http.StatusCreated, &ep)
	h.inbound(customer, "wamid.A1", "Hello")
	rcv.status = http.StatusInternalServerError
	h.inbound(customer, "wamid.A2", "Anyone?")
	h.deliverAll(me.Tenant.ID, worker, 1)
	var wh devportal.WebhookReport
	c.do("GET", "/internal/developers/webhook-usage?days=30", nil, http.StatusOK, &wh)
	if wh.Totals.Deliveries != 2 || wh.Totals.Retrying != 2 || wh.Totals.Attempts != 2 || len(wh.ByEndpoint) != 1 || wh.ByEndpoint[0].EndpointID != ep.ID || len(wh.Points) != 1 {
		t.Fatalf("webhook usage = %+v", wh)
	}

	// Another workspace sees none of it.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", "/internal/developers/logs", nil, http.StatusOK, &logs)
	other.do("GET", "/internal/developers/logs/summary", nil, http.StatusOK, &sum)
	if len(logs.Data) != 0 || sum.Calls != 0 {
		t.Fatalf("other workspace logs = %+v %+v", logs, sum)
	}

	// The daily retention job drops requests older than 30 days.
	err := h.db.InTenant(ctx, me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE api_request_log SET created_at = now() - interval '31 days' WHERE path = '/v1/templates'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rw := retention.NewWorker(h.db, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := rw.Work(ctx, &river.Job[retention.PurgeArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3}}); err != nil {
		t.Fatal(err)
	}
	var left int
	h.scalar(me.Tenant.ID, "SELECT count(*) FROM api_request_log", &left)
	if left != 3 {
		t.Fatalf("log rows left = %d", left)
	}
}
