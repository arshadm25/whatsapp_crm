package media

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSigner(t *testing.T) {
	s := NewSigner([]byte("secret"))
	tenant, id := uuid.New(), uuid.New()
	now := time.Unix(1_800_000_000, 0)
	q := s.Query(tenant, id, now.Add(LinkTTL))
	v, err := url.ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	exp, sig := v.Get("exp"), v.Get("sig")
	if !s.Check(tenant, id, exp, sig, now) {
		t.Fatal("fresh link rejected")
	}
	if s.Check(tenant, id, exp, sig, now.Add(LinkTTL+time.Second)) {
		t.Fatal("expired link accepted")
	}
	if s.Check(uuid.New(), id, exp, sig, now) || s.Check(tenant, uuid.New(), exp, sig, now) {
		t.Fatal("link accepted for another tenant or file")
	}
}

func TestKinds(t *testing.T) {
	cases := []struct {
		msgType, mime string
		ok            bool
	}{
		{"image", "image/jpeg", true},
		{"image", "image/webp", false},
		{"sticker", "image/webp", true},
		{"audio", "audio/ogg; codecs=opus", true},
		{"document", "image/png", true},
		{"video", "application/pdf", false},
		{"document", "application/x-msdownload", false},
	}
	for _, c := range cases {
		if got := Fits(c.msgType, c.mime); got != c.ok {
			t.Errorf("Fits(%s, %s) = %v", c.msgType, c.mime, got)
		}
	}
	if _, limit, _ := KindOf("image/png"); limit != 5<<20 {
		t.Errorf("image limit = %d", limit)
	}
}

func TestDetectType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if typ, err := detectType("", "photo.PNG", png); err != nil || typ != "image/png" {
		t.Errorf("by extension: %s %v", typ, err)
	}
	if _, err := detectType("image/jpeg", "photo.jpg", png); err == nil {
		t.Error("PNG content accepted as JPEG")
	}
	if typ, err := detectType("application/octet-stream", "notes.txt", []byte("hello")); err != nil || typ != "text/plain" {
		t.Errorf("text: %s %v", typ, err)
	}
}
