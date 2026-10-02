// Package media stores files sent and received over WhatsApp: uploads for sending
// (/v1/media), copies of inbound media taken from Meta on arrival, and the upload to Meta that
// sending a stored file needs.
package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Kinds of WhatsApp media, as message types.
const (
	KindImage    = "image"
	KindVideo    = "video"
	KindAudio    = "audio"
	KindDocument = "document"
	KindSticker  = "sticker"
)

const (
	mb = 1 << 20
	// MaxSize is the largest file accepted (documents).
	MaxSize = 100 * mb
)

// types lists the MIME types WhatsApp accepts, their kind and size limit.
var types = map[string]struct {
	kind  string
	limit int64
}{
	"image/jpeg":                    {KindImage, 5 * mb},
	"image/png":                     {KindImage, 5 * mb},
	"image/webp":                    {KindSticker, 500 << 10},
	"video/mp4":                     {KindVideo, 16 * mb},
	"video/3gpp":                    {KindVideo, 16 * mb},
	"audio/aac":                     {KindAudio, 16 * mb},
	"audio/amr":                     {KindAudio, 16 * mb},
	"audio/mpeg":                    {KindAudio, 16 * mb},
	"audio/mp4":                     {KindAudio, 16 * mb},
	"audio/ogg":                     {KindAudio, 16 * mb},
	"text/plain":                    {KindDocument, MaxSize},
	"application/pdf":               {KindDocument, MaxSize},
	"application/msword":            {KindDocument, MaxSize},
	"application/vnd.ms-excel":      {KindDocument, MaxSize},
	"application/vnd.ms-powerpoint": {KindDocument, MaxSize},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {KindDocument, MaxSize},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {KindDocument, MaxSize},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {KindDocument, MaxSize},
}

// NormalizeType strips parameters and lowercases a MIME type.
func NormalizeType(t string) string {
	if mt, _, err := mime.ParseMediaType(t); err == nil {
		return mt
	}
	return strings.ToLower(strings.TrimSpace(t))
}

// KindOf returns the WhatsApp kind of a MIME type and its size limit; ok is false for a type
// WhatsApp does not accept.
func KindOf(mimeType string) (kind string, limit int64, ok bool) {
	t, ok := types[NormalizeType(mimeType)]
	return t.kind, t.limit, ok
}

// Fits reports whether a stored file can be sent as the given message type. Any accepted file
// can go as a document; the other types need a matching kind.
func Fits(messageType, mimeType string) bool {
	kind, _, ok := KindOf(mimeType)
	return ok && (messageType == KindDocument || messageType == kind)
}

// Key is the object key of a file in the media bucket.
func Key(tenantID, mediaID uuid.UUID) string {
	return "tenant/" + tenantID.String() + "/" + mediaID.String()
}

// LinkTTL is how long a download link works.
const LinkTTL = 15 * time.Minute

// Signer makes and checks download links, which carry no session.
type Signer struct{ secret []byte }

func NewSigner(secret []byte) *Signer { return &Signer{secret: secret} }

func (s *Signer) mac(tenantID, mediaID uuid.UUID, exp int64) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("media-link\x00" + tenantID.String() + "\x00" + mediaID.String() + "\x00" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

// Query returns the query string of a link that expires at exp.
func (s *Signer) Query(tenantID, mediaID uuid.UUID, exp time.Time) string {
	e := exp.Unix()
	return "t=" + tenantID.String() + "&exp=" + strconv.FormatInt(e, 10) + "&sig=" + s.mac(tenantID, mediaID, e)
}

// Check verifies a link's signature and expiry at now.
func (s *Signer) Check(tenantID, mediaID uuid.UUID, exp, sig string, now time.Time) bool {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > e {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.mac(tenantID, mediaID, e)))
}
