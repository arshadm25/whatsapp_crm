package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238) with the settings every authenticator app supports: SHA-1, 6 digits, 30-second steps.
const (
	totpStep   = 30 * time.Second
	totpDigits = 6
	totpIssuer = "Ecogo WhatsApp"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret in base32, as authenticator apps expect.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// TOTPURI is the otpauth:// link an authenticator app reads from a QR code or a tap.
func TOTPURI(secret, email string) string {
	label := url.PathEscape(totpIssuer + ":" + email)
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		label, secret, url.QueryEscape(totpIssuer))
}

func totpCode(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, n%1_000_000)
}

// CheckTOTP accepts the code for the current step or the one before or after, to allow for
// clock drift on the phone.
func CheckTOTP(secret, code string, now time.Time) bool {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) == 0 {
		return false
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return false
	}
	step := uint64(now.Unix()) / uint64(totpStep/time.Second)
	for _, c := range []uint64{step - 1, step, step + 1} {
		if hmac.Equal([]byte(totpCode(key, c)), []byte(code)) {
			return true
		}
	}
	return false
}

// TOTPCode is the code an authenticator app shows for secret at now.
func TOTPCode(secret string, now time.Time) string {
	key, _ := b32.DecodeString(strings.ToUpper(secret))
	return totpCode(key, uint64(now.Unix())/uint64(totpStep/time.Second))
}
