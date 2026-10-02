// Package httpx holds the JSON request and response helpers shared by every HTTP handler,
// including the one error shape the public API promises: {"error": {"code", "message", ...}}.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Error is an API error with a stable machine-readable code (see api/openapi.yaml, Error).
type Error struct {
	Status        int    `json:"-"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Param         string `json:"param,omitempty"`
	MetaErrorCode int    `json:"meta_error_code,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(param, message string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: message, Param: param}
}

var (
	ErrUnauthorized = NewError(http.StatusUnauthorized, "unauthorized", "Sign in to continue.")
	ErrForbidden    = NewError(http.StatusForbidden, "forbidden", "You do not have access to this.")
	ErrNotFound     = NewError(http.StatusNotFound, "not_found", "Not found.")
)

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID tags each request with an ID, echoed in X-Request-Id and in error bodies.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = "req_" + hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func GetRequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Logger logs one structured line per request.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.Info("http request",
				"method", r.Method, "path", r.URL.Path, "status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", GetRequestID(r.Context()))
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// JSON writes v with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes err in the API error shape. Unknown errors become a logged 500.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		log.Error("internal error", "err", err, "path", r.URL.Path, "request_id", GetRequestID(r.Context()))
		apiErr = NewError(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Please try again.")
	}
	body := *apiErr
	body.RequestID = GetRequestID(r.Context())
	JSON(w, apiErr.Status, map[string]any{"error": body})
}

// Decode reads a JSON body of at most 1 MiB into v, rejecting unknown fields.
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return BadRequest("", "Request body is not valid JSON: "+err.Error())
	}
	return nil
}

// Handler adapts a handler that returns an error.
func Handler(log *slog.Logger, fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, r, log, err)
		}
	}
}
