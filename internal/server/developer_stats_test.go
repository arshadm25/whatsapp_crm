package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arshadm25/whatsapp_crm/internal/devportal"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

func TestDeveloperStatsAndWebhookSecrets(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()

	// API calls are counted per key, errors apart.
	var created devportal.CreatedKey
	c.do("POST", "/internal/developers/api-keys", map[string]any{"name": "Shop"}, http.StatusCreated, &created)
	key := "Bearer " + created.Key
	h.bearer(key, "GET", "/v1/phone-numbers", nil)
	h.bearer(key, "GET", "/v1/templates", nil)
	if resp, _ := h.bearer(key, "GET", "/v1/conversations/not-an-id", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad id = %d", resp.StatusCode)
	}
	var st devportal.Stats
	c.do("GET", "/internal/developers/stats", nil, http.StatusOK, &st)
	if st.APICalls != 3 || st.APIErrors != 1 || st.APIErrorRate == nil || len(st.ByKey) != 1 || st.ByKey[0].APIKeyID != created.ID {
		t.Fatalf("stats = %+v", st)
	}

	// Webhook deliveries feed the success rate; each one's payload can be read back.
	rcv := &receiver{status: http.StatusOK}
	srv := httptest.NewTLSServer(rcv)
	t.Cleanup(srv.Close)
	worker := webhooks.NewWorker(h.db, h.keys, srv.Client(), h.log)
	var ep webhooks.CreatedEndpoint
	c.do("POST", "/v1/webhook-endpoints", map[string]any{"url": srv.URL, "event_types": []string{"message.received"}}, http.StatusCreated, &ep)
	h.inbound(customer, "wamid.D1", "Hello")
	rcv.status = http.StatusInternalServerError
	h.inbound(customer, "wamid.D2", "Anyone?")
	rcv.status = http.StatusOK
	h.deliverAll(me.Tenant.ID, worker, 1)
	c.do("GET", "/internal/developers/stats", nil, http.StatusOK, &st)
	if st.WebhookDeliveries != 2 || st.WebhookSucceeded != 2 || st.WebhookSuccessRate == nil || *st.WebhookSuccessRate != 100 {
		t.Fatalf("webhook stats = %+v", st)
	}

	var page struct{ Data []webhooks.Delivery }
	c.do("GET", "/v1/webhook-endpoints/"+ep.ID.String()+"/deliveries", nil, http.StatusOK, &page)
	var d webhooks.DeliveryDetail
	c.do("GET", "/v1/webhook-endpoints/"+ep.ID.String()+"/deliveries/"+page.Data[0].ID.String(), nil, http.StatusOK, &d)
	var env struct{ Type string }
	if err := json.Unmarshal(d.Payload, &env); err != nil || env.Type != "message.received" || d.ID != page.Data[0].ID {
		t.Fatalf("delivery = %+v %s", d, d.Payload)
	}

	// The secret can be shown again and rotated; new deliveries use the new one.
	var sec struct{ Secret string }
	c.do("POST", "/v1/webhook-endpoints/"+ep.ID.String()+"/secret", nil, http.StatusOK, &sec)
	if sec.Secret != ep.Secret {
		t.Fatalf("revealed %q, want %q", sec.Secret, ep.Secret)
	}
	c.do("POST", "/v1/webhook-endpoints/"+ep.ID.String()+"/secret/rotate", nil, http.StatusOK, &sec)
	if sec.Secret == ep.Secret || !strings.HasPrefix(sec.Secret, "whsec_") {
		t.Fatalf("rotated = %q", sec.Secret)
	}
	n := len(rcv.got)
	h.inbound(customer, "wamid.D3", "Still there?")
	h.deliverAll(me.Tenant.ID, worker, 1)
	got := rcv.got[n]
	sig := got.header.Get("Ecogo-Signature")
	ts, _, _ := strings.Cut(strings.TrimPrefix(sig, "t="), ",")
	var unix int64
	_ = json.Unmarshal([]byte(ts), &unix)
	if want := webhooks.Sign([]byte(sec.Secret), time.Unix(unix, 0), got.body); sig != want {
		t.Fatal("delivery not signed with the rotated secret")
	}

	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("POST", "/v1/webhook-endpoints/"+ep.ID.String()+"/secret", nil, http.StatusNotFound, nil)
	other.do("GET", "/internal/developers/stats", nil, http.StatusOK, &st)
	if st.APICalls != 0 || st.WebhookDeliveries != 0 {
		t.Fatalf("other workspace stats = %+v", st)
	}
}
