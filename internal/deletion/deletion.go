// Package deletion handles Meta's data deletion callback, an App Review requirement: when a
// person removes the app in Facebook, Meta posts a signed request to /meta/data-deletion and
// expects a confirmation code and a page where the person can check the request's status.
package deletion

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

type Handler struct {
	db        *db.DB
	appSecret []byte
	baseURL   string // public origin that serves StatusPage, for example https://connect.ecogo.ai
	log       *slog.Logger
}

func NewHandler(d *db.DB, appSecret, baseURL string, log *slog.Logger) *Handler {
	return &Handler{db: d, appSecret: []byte(appSecret), baseURL: strings.TrimRight(baseURL, "/"), log: log}
}

var errBadRequest = errors.New("invalid signed_request")

// parseSignedRequest checks a Facebook signed_request ("signature.payload", both base64url) and
// returns the user ID it names.
func parseSignedRequest(secret []byte, signed string) (string, error) {
	sig, payload, ok := strings.Cut(signed, ".")
	if !ok || len(secret) == 0 {
		return "", errBadRequest
	}
	want, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sig, "="))
	if err != nil {
		return "", errBadRequest
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	if !hmac.Equal(want, mac.Sum(nil)) {
		return "", errBadRequest
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return "", errBadRequest
	}
	var v struct {
		Algorithm string `json:"algorithm"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.UserID == "" || !strings.EqualFold(v.Algorithm, "HMAC-SHA256") {
		return "", errBadRequest
	}
	return v.UserID, nil
}

// Callback records a request and answers with the status page URL and confirmation code.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID, err := parseSignedRequest(h.appSecret, r.PostForm.Get("signed_request"))
	if err != nil {
		h.log.Warn("data deletion: bad signature", "remote", r.RemoteAddr)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	code := hex.EncodeToString(b)
	err = h.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.InsertDeletionRequest(r.Context(), dbq.InsertDeletionRequestParams{ConfirmationCode: code, MetaUserID: userID})
		return err
	})
	if err != nil {
		h.log.Error("data deletion: store request", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"url": h.baseURL + "/v1/data-deletion/" + code, "confirmation_code": code,
	})
}

var statusText = map[string]string{
	"received":    "We received your request and will delete your data.",
	"in_progress": "We are deleting your data.",
	"completed":   "Your data has been deleted.",
}

// StatusPage is the public page Meta shows people to follow their request.
func (h *Handler) StatusPage(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var req dbq.DataDeletionRequest
	err := h.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		req, err = q.GetDeletionRequestByCode(r.Context(), code)
		return err
	})
	if db.IsNotFound(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.log.Error("data deletion: status", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`+
		`<title>Data deletion request</title><body style="font-family:system-ui;max-width:32rem;margin:3rem auto;padding:0 1rem">`+
		`<h1>Data deletion request</h1><p>Confirmation code: <code>`+html.EscapeString(req.ConfirmationCode)+`</code></p>`+
		`<p>`+html.EscapeString(statusText[req.Status])+`</p></body>`)
}
