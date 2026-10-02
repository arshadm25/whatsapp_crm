package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors for SHA-1 (8 digits there; the last 6 here).
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if !CheckTOTP(secret, want, time.Unix(unix, 0)) {
			t.Errorf("t=%d: %s rejected", unix, want)
		}
	}
	if CheckTOTP(secret, "287082", time.Unix(59+120, 0)) {
		t.Error("code accepted four steps late")
	}
	if CheckTOTP(secret, "28708", time.Unix(59, 0)) || CheckTOTP("not base32!", "287082", time.Unix(59, 0)) {
		t.Error("bad input accepted")
	}
	if s := NewTOTPSecret(); len(s) != 32 || !strings.Contains(TOTPURI(s, "a@b.in"), "secret="+s) {
		t.Errorf("secret %q", s)
	}
}
