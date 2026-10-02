package metaclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangeCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v24.0/oauth/access_token" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("client_id") != "app" || q.Get("client_secret") != "secret" || q.Get("code") != "abc" {
			t.Errorf("query = %v", q)
		}
		_, _ = w.Write([]byte(`{"access_token":"EAAG","token_type":"bearer"}`))
	}))
	defer srv.Close()

	tok, err := New(srv.URL, "v24.0", "app", "secret").ExchangeCode(context.Background(), "abc")
	if err != nil || tok != "EAAG" {
		t.Fatalf("ExchangeCode = %q, %v", tok, err)
	}
}

func TestErrorMappingAndRecorder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer EAAGsecret123" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid OAuth access token","type":"OAuthException","code":190,"error_subcode":463,"fbtrace_id":"AbC"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "v24.0", "app", "secret")
	var recorded *Error
	var recordedPath string
	c.OnError = func(_ context.Context, _, path string, e *Error) { recorded, recordedPath = e, path }

	err := c.SubscribeApp(context.Background(), "EAAGsecret123", "123")
	var me *Error
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if !me.IsTokenInvalid() || me.Subcode != 463 || me.FBTraceID != "AbC" || me.HTTPStatus != 400 {
		t.Fatalf("mapped error = %+v", me)
	}
	if recorded == nil || recordedPath != "/123/subscribed_apps" {
		t.Fatalf("recorder got %v at %q", recorded, recordedPath)
	}
	if strings.Contains(err.Error(), "EAAGsecret123") {
		t.Fatal("token leaked into error text")
	}
}

func TestRegisterAndSMBSyncBodies(t *testing.T) {
	var bodies []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		b["path"] = r.URL.Path
		bodies = append(bodies, b)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "v24.0", "app", "secret")
	if err := c.RegisterPhoneNumber(context.Background(), "tok", "555", "123456"); err != nil {
		t.Fatal(err)
	}
	if err := c.RequestSMBAppData(context.Background(), "tok", "555", SyncHistory); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["path"] != "/v24.0/555/register" || bodies[0]["pin"] != "123456" || bodies[0]["messaging_product"] != "whatsapp" {
		t.Fatalf("register body = %v", bodies[0])
	}
	if bodies[1]["path"] != "/v24.0/555/smb_app_data" || bodies[1]["sync_type"] != "history" {
		t.Fatalf("smb_app_data body = %v", bodies[1])
	}
}
