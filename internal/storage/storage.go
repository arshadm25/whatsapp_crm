// Package storage keeps media files in an S3-compatible bucket (MinIO in the cluster), or in a
// local directory for development and tests.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/arshadm25/whatsapp_crm/internal/config"
)

// Open returns the bucket store when an endpoint is configured, otherwise the directory store.
func Open(ctx context.Context, c config.Storage) (Store, error) {
	if c.Endpoint == "" {
		return NewDir(c.Dir)
	}
	return NewS3(ctx, c.Endpoint, c.AccessKey, c.SecretKey, c.Bucket, c.UseSSL)
}

// Store saves and reads objects by key.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// ErrNotFound is returned by Get for a missing key.
var ErrNotFound = errors.New("storage: object not found")

// S3 stores objects in one bucket of an S3-compatible service.
type S3 struct {
	client *minio.Client
	bucket string
}

// NewS3 connects to endpoint (host:port, no scheme) and creates the bucket if it is missing.
func NewS3(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: useSSL})
	if err != nil {
		return nil, err
	}
	exists, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("storage: check bucket %q: %w", bucket, err)
	}
	if !exists {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("storage: create bucket %q: %w", bucket, err)
		}
	}
	return &S3{client: c, bucket: bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; Stat surfaces a missing key now rather than on first read.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return obj, nil
}

// Dir stores objects as files under a directory.
type Dir struct{ root string }

func NewDir(root string) (*Dir, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &Dir{root: root}, nil
}

func (d *Dir) path(key string) (string, error) {
	p := filepath.Join(d.root, filepath.FromSlash(key))
	if !strings.HasPrefix(p, filepath.Clean(d.root)+string(filepath.Separator)) {
		return "", fmt.Errorf("storage: bad key %q", key)
	}
	return p, nil
}

func (d *Dir) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

func (d *Dir) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}
