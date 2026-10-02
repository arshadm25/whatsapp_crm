package server_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestMetricsEndpoint(t *testing.T) {
	h := newHarness(t)
	c, _, phone := h.connected()
	c.do("GET", "/v1/phone-numbers/"+phone.ID.String(), nil, http.StatusOK, nil)

	resp, raw := h.bearer("", "GET", "/metrics", nil)
	body := string(raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics = %d", resp.StatusCode)
	}
	for _, want := range []string{
		// The route pattern is a label, not the URL with its ID.
		`ecogo_http_requests_total{method="GET",route="/v1/phone-numbers/{id}",status="200"}`,
		"ecogo_http_request_duration_seconds_bucket",
		"ecogo_db_pool_max_connections",
		"ecogo_meta_webhook_backlog",
		"ecogo_meta_api_errors_1h",
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
	if strings.Contains(body, phone.ID.String()) {
		t.Error("metrics leak a phone number ID")
	}
	if strings.Contains(body, `route="/metrics"`) || strings.Contains(body, `route="/healthz"`) {
		t.Error("probe and metrics routes are counted")
	}
}
