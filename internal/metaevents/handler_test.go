package metaevents

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type fakeQueue struct {
	got []ProcessArgs
	err error
}

func (f *fakeQueue) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.got = append(f.got, args.(ProcessArgs))
	return &rivertype.JobInsertResult{}, nil
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func newTestHandler(q *fakeQueue) *Handler {
	return NewHandler("app-secret", "verify-me", q, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHandshake(t *testing.T) {
	h := newTestHandler(&fakeQueue{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/meta?hub.mode=subscribe&hub.verify_token=verify-me&hub.challenge=12345", nil))
	if rec.Code != 200 || rec.Body.String() != "12345" {
		t.Fatalf("handshake = %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/meta?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=12345", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token = %d, want 403", rec.Code)
	}
}

func TestReceiveChecksSignatureAndEnqueues(t *testing.T) {
	body := `{"object":"whatsapp_business_account","entry":[]}`
	q := &fakeQueue{}
	h := newTestHandler(q)

	for name, sig := range map[string]string{
		"missing":      "",
		"wrong secret": sign("other", body),
		"not hex":      "sha256=zz",
		"sha1 format":  "sha1=abc",
	} {
		req := httptest.NewRequest("POST", "/meta", strings.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", sig)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}
	if len(q.got) != 0 {
		t.Fatal("unsigned delivery was enqueued")
	}

	req := httptest.NewRequest("POST", "/meta", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("app-secret", body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || len(q.got) != 1 {
		t.Fatalf("signed delivery: status %d, enqueued %d", rec.Code, len(q.got))
	}
	sum := sha256.Sum256([]byte(body))
	if q.got[0].PayloadHash != hex.EncodeToString(sum[:]) || string(q.got[0].Payload) != body {
		t.Fatalf("job args = %+v", q.got[0])
	}
}

func TestReceiveReturns500WhenQueueFails(t *testing.T) {
	body := `{"object":"whatsapp_business_account"}`
	h := newTestHandler(&fakeQueue{err: errors.New("db down")})
	req := httptest.NewRequest("POST", "/meta", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("app-secret", body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 so Meta retries", rec.Code)
	}
}
