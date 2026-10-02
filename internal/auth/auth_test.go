package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckPassword(h, "correct horse battery staple"); !ok || err != nil {
		t.Fatalf("right password rejected: %v", err)
	}
	if ok, _ := CheckPassword(h, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestSignedToken(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	id := uuid.New()
	now := time.Now()
	tok := signToken(secret, "verify_email", id, now.Add(time.Hour))

	if got, err := verifyToken(secret, "verify_email", tok, now); err != nil || got != id {
		t.Fatalf("verify = %v, %v", got, err)
	}
	if _, err := verifyToken(secret, "reset_password", tok, now); err == nil {
		t.Fatal("token accepted for another purpose")
	}
	if _, err := verifyToken(secret, "verify_email", tok, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := verifyToken([]byte("another-secret-another-secret-xx"), "verify_email", tok, now); err == nil {
		t.Fatal("token accepted under another secret")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Sharma Sweets & Co.": "sharma-sweets-co",
		"  ":                  "workspace",
		"ಕನ್ನಡ ಅಂಗಡಿ":         "workspace",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
