package envelope

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func key(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	kr, err := NewKeyring(map[int][]byte{1: key(t)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	s, err := kr.Seal([]byte("EAAG-token"), []byte("tenant-a"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(s.Ciphertext, []byte("EAAG")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	got, err := kr.Open(s, []byte("tenant-a"))
	if err != nil || string(got) != "EAAG-token" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := kr.Open(s, []byte("tenant-b")); err == nil {
		t.Fatal("ciphertext opened with the wrong associated data")
	}
}

func TestRotationKeepsOldCiphertextReadable(t *testing.T) {
	k1, k2 := key(t), key(t)
	old, _ := NewKeyring(map[int][]byte{1: k1}, 1)
	blob, err := old.SealCompact([]byte("123456"), nil)
	if err != nil {
		t.Fatal(err)
	}
	rotated, _ := NewKeyring(map[int][]byte{1: k1, 2: k2}, 2)
	got, err := rotated.OpenCompact(blob, nil)
	if err != nil || string(got) != "123456" {
		t.Fatalf("OpenCompact = %q, %v", got, err)
	}
	fresh, _ := rotated.Seal([]byte("x"), nil)
	if fresh.MasterKeyVersion != 2 {
		t.Fatalf("new seal used version %d, want 2", fresh.MasterKeyVersion)
	}
}
