package httpx

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Limit parses a page size: 25 by default, at most 100.
func Limit(v string) (int32, error) {
	if v == "" {
		return 25, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 100 {
		return 0, BadRequest("limit", "limit must be between 1 and 100.")
	}
	return int32(n), nil
}

// EncodeCursor makes an opaque keyset cursor from a row's sort time and ID.
func EncodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

// DecodeCursor reverses EncodeCursor.
func DecodeCursor(s string) (time.Time, uuid.UUID, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	at, rest, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	t, err1 := time.Parse(time.RFC3339Nano, at)
	id, err2 := uuid.Parse(rest)
	return t, id, err1 == nil && err2 == nil
}
