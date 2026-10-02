package server_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/deletion"
	"github.com/arshadm25/whatsapp_crm/internal/server"
)

func signedRequest(secret, userID string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"algorithm":"HMAC-SHA256","user_id":"` + userID + `"}`))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) + "." + payload
}

func TestDataDeletionCallback(t *testing.T) {
	h := newHarness(t)
	ingest := httptest.NewServer(server.NewIngest(h.db, http.NotFoundHandler(), deletion.NewHandler(h.db, "app-secret", h.api.URL, h.log), h.log))
	t.Cleanup(ingest.Close)

	post := func(signed string) (int, string) {
		resp, err := http.PostForm(ingest.URL+"/meta/data-deletion", url.Values{"signed_request": {signed}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// A request signed with another secret, or with no signature, is refused.
	if status, _ := post(signedRequest("wrong", "1234")); status != http.StatusUnauthorized {
		t.Errorf("wrong secret = %d, want 401", status)
	}
	if status, _ := post("garbage"); status != http.StatusUnauthorized {
		t.Errorf("garbage = %d, want 401", status)
	}

	status, body := post(signedRequest("app-secret", "1234"))
	var raw map[string]string
	_ = json.Unmarshal([]byte(body), &raw)
	if status != http.StatusOK || raw["confirmation_code"] == "" || raw["url"] != h.api.URL+"/v1/data-deletion/"+raw["confirmation_code"] {
		t.Fatalf("callback = %d %s", status, body)
	}

	// The status page is public and follows the request.
	resp, page := h.bearer("", "GET", "/v1/data-deletion/"+raw["confirmation_code"], nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), "We received your request") {
		t.Fatalf("status page = %d %s", resp.StatusCode, page)
	}
	if resp, _ := h.bearer("", "GET", "/v1/data-deletion/nope", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown code = %d, want 404", resp.StatusCode)
	}

	// Staff see the request and mark it done.
	staff := h.platformAdmin("ops@ecogo.co.in")
	var list struct {
		Data []struct {
			ID         string `json:"id"`
			MetaUserID string `json:"meta_user_id"`
			Status     string `json:"status"`
		}
	}
	staff.do("GET", "/internal/admin/deletion-requests", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.Data[0].MetaUserID != "1234" || list.Data[0].Status != "received" {
		t.Fatalf("list = %+v", list.Data)
	}
	staff.do("POST", "/internal/admin/deletion-requests/"+list.Data[0].ID+"/status", map[string]string{"status": "bogus"}, http.StatusBadRequest, nil)
	staff.do("POST", "/internal/admin/deletion-requests/"+list.Data[0].ID+"/status", map[string]string{"status": "completed"}, http.StatusOK, nil)
	if _, page := h.bearer("", "GET", "/v1/data-deletion/"+raw["confirmation_code"], nil); !strings.Contains(string(page), "has been deleted") {
		t.Errorf("completed page = %s", page)
	}
}
