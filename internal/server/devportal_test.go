package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/devportal"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

// bearer calls the api with an API key and no cookies, as an integration would.
func (h *harness) bearer(key, method, path string, body any) (*http.Response, []byte) {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.api.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestAPIKeys(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	h.inbound(customer, "wamid.IN1", "Hi")

	var created devportal.CreatedKey
	c.do("POST", "/internal/developers/api-keys", map[string]any{"name": "Shopify store", "phone_number_id": phone.ID}, http.StatusCreated, &created)
	if !strings.HasPrefix(created.Key, "eco_live_") || !strings.HasPrefix(created.Key, created.Prefix) || len(created.Key) < 40 {
		t.Fatalf("created key = %+v", created)
	}
	var list struct{ Data []json.RawMessage }
	c.do("GET", "/internal/developers/api-keys", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || strings.Contains(string(list.Data[0]), created.Key) {
		t.Fatalf("list = %s", list.Data)
	}
	key := "Bearer " + created.Key

	// The key reads and sends through /v1 without cookies or a CSRF token.
	if resp, raw := h.bearer(key, "GET", "/v1/phone-numbers", nil); resp.StatusCode != http.StatusOK ||
		resp.Header.Get("X-RateLimit-Limit") != "5" || !strings.Contains(string(raw), phone.ID.String()) {
		t.Fatalf("GET with key = %d %v %s", resp.StatusCode, resp.Header, raw)
	}
	resp, raw := h.bearer(key, "POST", "/v1/messages", text(phone.ID, "Sent from the API"))
	var msg messaging.Message
	_ = json.Unmarshal(raw, &msg)
	if resp.StatusCode != http.StatusAccepted || msg.Origin != "api" {
		t.Fatalf("send with key = %d %s", resp.StatusCode, raw)
	}
	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		var user *uuid.UUID
		if err := tx.QueryRow(context.Background(), "SELECT sent_by_user_id FROM messages WHERE id = $1", msg.ID).Scan(&user); err != nil {
			return err
		}
		if user != nil {
			t.Errorf("sent_by_user_id = %v, want null for an API key", user)
		}
		var used bool
		if err := tx.QueryRow(context.Background(), "SELECT last_used_at IS NOT NULL FROM api_keys WHERE id = $1", created.ID).Scan(&used); err != nil {
			return err
		}
		if !used {
			t.Error("last_used_at not set")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Missing, malformed and unknown keys are refused; keys never open dashboard endpoints.
	for _, auth := range []string{"", "Basic abc", "Bearer eco_live_nope", "Bearer " + strings.ToUpper(created.Key)} {
		if resp, _ := h.bearer(auth, "GET", "/v1/phone-numbers", nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q = %d, want 401", auth, resp.StatusCode)
		}
	}
	if resp, _ := h.bearer(key, "GET", "/internal/developers/api-keys", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("key on /internal = %d, want 401", resp.StatusCode)
	}
	// "me" means nothing for a key.
	if resp, _ := h.bearer(key, "GET", "/v1/conversations?assignee_id=me", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("assignee_id=me with a key = %d", resp.StatusCode)
	}

	// The limit (5 per second in tests) answers 429 with Retry-After once spent.
	limited := false
	for i := 0; i < 8 && !limited; i++ {
		resp, raw := h.bearer(key, "GET", "/v1/phone-numbers", nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = resp.Header.Get("Retry-After") != "" && strings.Contains(string(raw), "rate_limited")
		}
	}
	if !limited {
		t.Fatal("no 429 after spending the rate limit")
	}

	// A revoked key stops working; revoking twice is a 404.
	c.do("DELETE", "/internal/developers/api-keys/"+created.ID.String(), nil, http.StatusNoContent, nil)
	c.do("DELETE", "/internal/developers/api-keys/"+created.ID.String(), nil, http.StatusNotFound, nil)
	if resp, _ := h.bearer(key, "GET", "/v1/templates", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked key = %d", resp.StatusCode)
	}

	// Another workspace's key sees only its own data.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	var otherKey devportal.CreatedKey
	other.do("POST", "/internal/developers/api-keys", map[string]any{"name": "x"}, http.StatusCreated, &otherKey)
	if resp, _ := h.bearer("Bearer "+otherKey.Key, "GET", "/v1/messages/"+msg.ID.String(), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other tenant's key read a message: %d", resp.StatusCode)
	}
	other.do("POST", "/internal/developers/api-keys", map[string]any{"name": "x", "phone_number_id": phone.ID}, http.StatusBadRequest, nil)
	other.do("POST", "/internal/developers/api-keys", map[string]any{"name": " "}, http.StatusBadRequest, nil)
}
