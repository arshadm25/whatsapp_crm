package media

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

var errTooLarge = errors.New("media: file too large")

// spooled is a file copied to local disk, so its size and hash are known before it is stored.
type spooled struct {
	f      *os.File
	size   int64
	sha256 []byte
	head   []byte // first bytes, for content sniffing
}

// spool copies r to a temporary file, refusing more than limit bytes.
func spool(r io.Reader, limit int64) (*spooled, error) {
	f, err := os.CreateTemp("", "ecogo-media-*")
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	if err == nil && n > limit {
		err = errTooLarge
	}
	if err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	s := &spooled{f: f, size: n, sha256: h.Sum(nil), head: make([]byte, 512)}
	k, _ := f.ReadAt(s.head, 0)
	s.head = s.head[:k]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *spooled) Read(p []byte) (int, error) { return s.f.Read(p) }

func (s *spooled) Close() {
	s.f.Close()
	os.Remove(s.f.Name())
}
