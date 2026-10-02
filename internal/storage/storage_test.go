package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.Put(ctx, "tenant/a/b.jpg", strings.NewReader("hello"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	r, err := d.Get(ctx, "tenant/a/b.jpg")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "hello" {
		t.Fatalf("got %q", b)
	}
	if err := d.Delete(ctx, "tenant/a/b.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctx, "tenant/a/b.jpg"); err != nil {
		t.Fatalf("deleting a missing object: %v", err)
	}
	if _, err := d.Get(ctx, "tenant/a/b.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := d.Get(ctx, "tenant/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := d.Put(ctx, "../escape", strings.NewReader("x"), 1, ""); err == nil {
		t.Fatal("key outside the root was accepted")
	}
}
