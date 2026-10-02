package metaevents

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

// maxBody caps a webhook delivery; Meta's payloads are far smaller.
const maxBody = 3 << 20

// Enqueuer is the part of the River client the receiver needs.
type Enqueuer interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Handler is Meta's webhook callback (https://hooks.whatsapp.ecogo.co.in/meta).
type Handler struct {
	appSecret   []byte
	verifyToken string
	jobs        Enqueuer
	log         *slog.Logger
	now         func() time.Time
}

func NewHandler(appSecret, verifyToken string, jobs Enqueuer, log *slog.Logger) *Handler {
	return &Handler{appSecret: []byte(appSecret), verifyToken: verifyToken, jobs: jobs, log: log, now: time.Now}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.verify(w, r)
	case http.MethodPost:
		h.receive(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// verify answers the subscription handshake Meta sends when the callback URL is saved.
func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	token := q.Get("hub.verify_token")
	if q.Get("hub.mode") != "subscribe" || h.verifyToken == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(h.verifyToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, q.Get("hub.challenge"))
}

// receive checks the signature over the raw body, enqueues it, and returns 200. If the enqueue
// fails it returns 500 so Meta retries the delivery later.
func (h *Handler) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !ValidSignature(h.appSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		h.log.Warn("meta webhook: bad signature", "remote", r.RemoteAddr)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256(body)
	args := ProcessArgs{Payload: body, PayloadHash: hex.EncodeToString(sum[:]), ReceivedAt: h.now().UTC()}
	if _, err := h.jobs.Insert(r.Context(), args, nil); err != nil {
		h.log.Error("meta webhook: enqueue failed", "err", err)
		http.Error(w, "try again", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ValidSignature checks Meta's X-Hub-Signature-256 header ("sha256=<hex HMAC of the body>").
func ValidSignature(secret, body []byte, header string) bool {
	if len(secret) == 0 {
		return false
	}
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

// ProcessArgs is the River job for one webhook delivery. The hash is taken over the raw bytes
// (River stores args as jsonb, which does not keep the original byte layout).
type ProcessArgs struct {
	Payload     json.RawMessage `json:"payload"`
	PayloadHash string          `json:"payload_hash"`
	ReceivedAt  time.Time       `json:"received_at"`
}

func (ProcessArgs) Kind() string { return "meta_event" }

func (ProcessArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMetaEvents, MaxAttempts: 10}
}
