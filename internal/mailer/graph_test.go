package mailer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
)

func TestGraphSendsFromTheMailboxAndReusesTheToken(t *testing.T) {
	var signIns, sends atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/tenant-1/oauth2/v2.0/token":
			signIns.Add(1)
			_ = r.ParseForm()
			if r.PostForm.Get("client_id") != "client-1" || r.PostForm.Get("client_secret") != "s3cret" ||
				r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("scope") != "https://graph.microsoft.com/.default" {
				http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case r.URL.Path == "/v1.0/users/no-reply@ecogo.co.in/sendMail":
			sends.Add(1)
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body struct {
				Message struct {
					Subject string `json:"subject"`
					Body    struct {
						ContentType string `json:"contentType"`
						Content     string `json:"content"`
					} `json:"body"`
					From struct {
						EmailAddress struct{ Address, Name string } `json:"emailAddress"`
					} `json:"from"`
					ToRecipients []struct {
						EmailAddress struct{ Address string } `json:"emailAddress"`
					} `json:"toRecipients"`
				} `json:"message"`
				SaveToSentItems bool `json:"saveToSentItems"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			m := body.Message
			if m.Subject != "Verify your email" || m.Body.ContentType != "Text" || !strings.Contains(m.Body.Content, "Hello") ||
				m.From.EmailAddress.Name != "Ecogo Connect" || len(m.ToRecipients) != 1 || m.ToRecipients[0].EmailAddress.Address != "asha@example.com" {
				t.Errorf("message = %+v", m)
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	g := mailer.New(config.Mail{
		Provider: "microsoft365",
		From:     "Ecogo Connect <no-reply@ecogo.co.in>",
		M365:     config.M365{TenantID: "tenant-1", ClientID: "client-1", ClientSecret: "s3cret", LoginBaseURL: srv.URL, GraphBaseURL: srv.URL},
	})
	for i := 0; i < 2; i++ {
		if err := g.Send(context.Background(), mailer.Message{To: "asha@example.com", Subject: "Verify your email", Text: "Hello"}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if signIns.Load() != 1 || sends.Load() != 2 {
		t.Errorf("sign-ins = %d, sends = %d; want 1 and 2", signIns.Load(), sends.Load())
	}
}

func TestGraphReportsMicrosoftErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"ErrorAccessDenied","message":"Access is denied."}}`))
	}))
	defer srv.Close()
	g := mailer.New(config.Mail{
		Provider: "microsoft365", From: "no-reply@ecogo.co.in",
		M365: config.M365{TenantID: "t", ClientID: "c", ClientSecret: "s", LoginBaseURL: srv.URL, GraphBaseURL: srv.URL},
	})
	err := g.Send(context.Background(), mailer.Message{To: "a@example.com", Subject: "Hi", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "ErrorAccessDenied") {
		t.Fatalf("err = %v, want Microsoft's error code", err)
	}
	if err := g.Send(context.Background(), mailer.Message{To: "a@example.com\r\nBcc: x@example.com", Subject: "Hi", Text: "x"}); err == nil {
		t.Fatal("header injection was not rejected")
	}
}
