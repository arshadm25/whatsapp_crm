package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAnswer(t *testing.T) {
	a, c, h, ok := ParseAnswer("Sure {\"answer\":\" Hi \",\"confidence\":1.7,\"handoff\":true} ok")
	if !ok || a != "Hi" || c != 1 || !h {
		t.Fatalf("got %q %v %v %v", a, c, h, ok)
	}
	for _, bad := range []string{"", "no json", `{"answer":"","confidence":1}`, `{"answer":`} {
		if _, _, _, ok := ParseAnswer(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, c, _, _ := ParseAnswer(`{"answer":"x","confidence":-3}`); c != 0 {
		t.Errorf("negative confidence = %v", c)
	}
}

func TestChunkAndWords(t *testing.T) {
	long := strings.Repeat("Sweets are made fresh every morning. ", 80)
	chunks := Chunk(long)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c)) > 1200 {
			t.Errorf("chunk of %d runes", len([]rune(c)))
		}
	}
	if len(Chunk("   ")) != 0 {
		t.Error("blank text gave chunks")
	}
	w := Words("When do you OPEN, today?")
	if TSQuery(w) == "" || len(w) == 0 {
		t.Fatalf("words = %v", w)
	}
	if Score("We open at nine", []string{"open", "nine"}) <= Score("We open", []string{"open", "nine"}) {
		t.Error("more matching words should score higher")
	}
}

func TestHTMLToText(t *testing.T) {
	title, text := HTMLToText(`<html><head><title>Shop</title><style>p{}</style></head><body><script>x()</script><h1>Hi</h1><p>A &amp; B</p></body></html>`)
	if title != "Shop" || strings.Contains(text, "x()") || !strings.Contains(text, "A & B") {
		t.Fatalf("title = %q text = %q", title, text)
	}
}

func TestCheckURLAndFetcherBlockInternalAddresses(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1/", "http://169.254.169.254/latest", "http://10.0.0.5/", "http://[::1]/", "https://example.com:8443/", "ftp://example.com/", "https://user:pw@example.com/"} {
		if _, err := CheckURL(u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	if _, err := CheckURL("https://example.com/menu"); err != nil {
		t.Errorf("public address refused: %v", err)
	}
	// A name that resolves to a local server is stopped at connection time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<p>secret page text here</p>")) }))
	defer srv.Close()
	local := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if _, _, err := (Fetcher{}).Page(context.Background(), local); err == nil {
		t.Error("fetched a local server")
	}
	if _, text, err := (Fetcher{AllowPrivate: true}).Page(context.Background(), srv.URL); err != nil || text != "secret page text here" {
		t.Errorf("allowed fetch = %q, %v", text, err)
	}
}
