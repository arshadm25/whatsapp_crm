package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Signed tokens are stateless HMAC tokens for links in emails: purpose.userID.expiry.signature.

var errBadToken = errors.New("auth: invalid or expired link")

func signToken(secret []byte, purpose string, userID uuid.UUID, expires time.Time) string {
	payload := purpose + "." + userID.String() + "." + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyToken(secret []byte, purpose, token string, now time.Time) (uuid.UUID, error) {
	p64, s64, ok := strings.Cut(token, ".")
	if !ok {
		return uuid.Nil, errBadToken
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p64)
	sig, err2 := base64.RawURLEncoding.DecodeString(s64)
	if err1 != nil || err2 != nil {
		return uuid.Nil, errBadToken
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return uuid.Nil, errBadToken
	}
	parts := strings.Split(string(payload), ".")
	if len(parts) != 3 || parts[0] != purpose {
		return uuid.Nil, errBadToken
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() > exp {
		return uuid.Nil, errBadToken
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.Nil, errBadToken
	}
	return id, nil
}
