package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
	"github.com/arshadm25/whatsapp_crm/internal/storage"
)

// Service serves /v1/media.
type Service struct {
	db     *db.DB
	store  storage.Store
	signer *Signer
	log    *slog.Logger
	now    func() time.Time
}

func NewService(d *db.DB, store storage.Store, signer *Signer, log *slog.Logger) *Service {
	return &Service{db: d, store: store, signer: signer, log: log, now: time.Now}
}

// Routes are the authenticated routes under /v1/media.
func (s *Service) Routes(r chi.Router) {
	r.Post("/", httpx.Handler(s.log, s.upload))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
}

// Content serves GET /v1/media/{id}/content, the signed download link. It carries no session,
// so it sits outside the authenticated routes.
func (s *Service) Content() http.HandlerFunc { return httpx.Handler(s.log, s.content) }

// Media matches the Media schema in api/openapi.yaml.
type Media struct {
	ID        uuid.UUID `json:"id"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	Filename  *string   `json:"filename"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) view(r *http.Request, m dbq.Medium) Media {
	link := baseURL(r) + "/v1/media/" + m.ID.String() + "/content?" + s.signer.Query(m.TenantID, m.ID, s.now().Add(LinkTTL))
	return Media{ID: m.ID, MimeType: m.MimeType, SizeBytes: m.SizeBytes, Filename: m.Filename, URL: link, CreatedAt: m.CreatedAt}
}

// baseURL is the origin the caller used, so the link works from the dashboard and the API host.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func tooLarge(limit int64) error {
	return &httpx.Error{Status: http.StatusRequestEntityTooLarge, Code: "media_too_large", Param: "file",
		Message: "The file is larger than WhatsApp allows for its type (" + humanSize(limit) + ")."}
}

func humanSize(n int64) string {
	if n >= mb {
		return strconv.FormatInt(n/mb, 10) + " MB"
	}
	return strconv.FormatInt(n>>10, 10) + " KB"
}

func (s *Service) upload(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, MaxSize+mb)
	mr, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("file", "Send the file as multipart/form-data with the fields file and phone_number_id.")
	}

	var (
		phoneID  uuid.UUID
		file     *spooled
		declared string
		filename string
	)
	defer func() {
		if file != nil {
			file.Close()
		}
	}()
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return tooLarge(MaxSize)
		}
		if err != nil {
			return httpx.BadRequest("file", "The multipart body could not be read.")
		}
		switch part.FormName() {
		case "phone_number_id":
			b, _ := io.ReadAll(io.LimitReader(part, 100))
			if phoneID, err = uuid.Parse(strings.TrimSpace(string(b))); err != nil {
				return httpx.BadRequest("phone_number_id", "phone_number_id must be a phone number ID.")
			}
		case "file":
			if file != nil {
				return httpx.BadRequest("file", "Send one file per request.")
			}
			declared, filename = part.Header.Get("Content-Type"), part.FileName()
			file, err = spool(part, MaxSize)
			if errors.Is(err, errTooLarge) || errors.As(err, &mbe) {
				return tooLarge(MaxSize)
			}
			if err != nil {
				return err
			}
		}
		part.Close()
	}
	if file == nil || file.size == 0 {
		return httpx.BadRequest("file", "file is required and must not be empty.")
	}
	if phoneID == uuid.Nil {
		return httpx.BadRequest("phone_number_id", "phone_number_id is required.")
	}
	mimeType, err := detectType(declared, filename, file.head)
	if err != nil {
		return err
	}
	if _, limit, _ := KindOf(mimeType); file.size > limit {
		return tooLarge(limit)
	}
	if utf8.RuneCountInString(filename) > 240 {
		filename = string([]rune(filename)[:240])
	}

	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		_, err := q.GetSendingNumber(ctx, phoneID)
		return err
	})
	if db.IsNotFound(err) {
		return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Param: "phone_number_id", Message: "Phone number not found."}
	}
	if err != nil {
		return err
	}

	id := db.NewID()
	key := Key(p.TenantID, id)
	if err := s.store.Put(ctx, key, file, file.size, mimeType); err != nil {
		return err
	}
	var m dbq.Medium
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		m, err = q.InsertMedia(ctx, dbq.InsertMediaParams{
			ID: id, TenantID: p.TenantID, PhoneNumberID: &phoneID, StorageKey: key, MimeType: mimeType,
			SizeBytes: file.size, Sha256: file.sha256, Filename: nonEmpty(filepath.Base(filename)),
		})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, s.view(r, m))
	return nil
}

// detectType picks the file's MIME type from the part header, then the file name, and checks
// image content really is that kind of image.
func detectType(declared, filename string, head []byte) (string, error) {
	t := NormalizeType(declared)
	if t == "" || t == "application/octet-stream" {
		t = NormalizeType(mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))))
	}
	if t == "" {
		t = NormalizeType(http.DetectContentType(head))
	}
	if _, _, ok := KindOf(t); !ok {
		return "", httpx.BadRequest("file", "WhatsApp does not accept this file type. Send JPEG or PNG images, MP4 or 3GP video, AAC, AMR, MP3, M4A or OGG audio, WebP stickers, or PDF, Office or text documents.")
	}
	if strings.HasPrefix(t, "image/") && NormalizeType(http.DetectContentType(head)) != t {
		return "", httpx.BadRequest("file", "The file content does not match its type "+t+".")
	}
	return t, nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var m dbq.Medium
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		m, err = q.GetMedia(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, s.view(r, m))
	return nil
}

var errLink = httpx.NewError(http.StatusForbidden, "forbidden", "This download link is invalid or has expired. Get a new one from GET /v1/media/{media_id}.")

func (s *Service) content(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	q := r.URL.Query()
	tenantID, err := uuid.Parse(q.Get("t"))
	if err != nil || !s.signer.Check(tenantID, id, q.Get("exp"), q.Get("sig"), s.now()) {
		return errLink
	}
	var m dbq.Medium
	err = s.db.InTenant(r.Context(), tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		m, err = q.GetMedia(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	rc, err := s.store.Get(r.Context(), m.StorageKey)
	if errors.Is(err, storage.ErrNotFound) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", m.MimeType)
	h.Set("Content-Length", strconv.FormatInt(m.SizeBytes, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=900")
	disposition := "inline"
	if kind, _, _ := KindOf(m.MimeType); kind == KindDocument || kind == "" {
		disposition = "attachment"
	}
	if m.Filename != nil {
		disposition = mime.FormatMediaType(disposition, map[string]string{"filename": *m.Filename})
	}
	h.Set("Content-Disposition", disposition)
	_, _ = io.Copy(w, rc)
	return nil
}

func nonEmpty(s string) *string {
	if s == "" || s == "." || s == "/" {
		return nil
	}
	return &s
}

// Uploader gets a Meta media ID for a stored file, uploading it when needed.
type Uploader struct {
	store storage.Store
	meta  Meta
	now   func() time.Time
}

// Meta is the part of the Graph client this package uses.
type Meta interface {
	UploadMedia(ctx context.Context, token, phoneNumberID, mimeType, filename string, file io.Reader) (string, error)
	GetMedia(ctx context.Context, token, mediaID string) (*metaclient.MediaInfo, error)
	DownloadMedia(ctx context.Context, token, link string) (io.ReadCloser, error)
}

func NewUploader(store storage.Store, meta Meta) *Uploader {
	return &Uploader{store: store, meta: meta, now: time.Now}
}

// reuseFor is how long an upload is reused; Meta deletes it after 30 days.
const reuseFor = 25 * 24 * time.Hour

// MetaID returns the Meta media ID to send m from a phone number. uploaded reports a fresh
// upload, which the caller records with MarkMediaUploaded.
func (u *Uploader) MetaID(ctx context.Context, token string, m dbq.Medium, phoneID uuid.UUID, metaPhoneID string) (id string, uploaded bool, err error) {
	if m.MetaMediaID != nil && m.MetaUploadedAt != nil && m.PhoneNumberID != nil && *m.PhoneNumberID == phoneID &&
		u.now().Sub(*m.MetaUploadedAt) < reuseFor {
		return *m.MetaMediaID, false, nil
	}
	rc, err := u.store.Get(ctx, m.StorageKey)
	if err != nil {
		return "", false, err
	}
	defer rc.Close()
	name := "file"
	if m.Filename != nil {
		name = *m.Filename
	}
	id, err = u.meta.UploadMedia(ctx, token, metaPhoneID, m.MimeType, name, rc)
	return id, err == nil, err
}
