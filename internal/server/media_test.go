package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/media"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

// upload posts a file to /v1/media.
func (c *client) upload(phone uuid.UUID, filename, contentType string, data []byte, want int, out any) {
	c.h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("phone_number_id", phone.String())
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	if contentType != "" {
		hdr.Set("Content-Type", contentType)
	}
	part, _ := mw.CreatePart(hdr)
	_, _ = part.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.h.api.URL+"/v1/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	u, _ := url.Parse(c.h.api.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == auth.CSRFCookie {
			req.Header.Set(auth.CSRFHeader, ck.Value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.h.t.Fatalf("POST /v1/media = %d, want %d: %s", resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.h.t.Fatal(err)
		}
	}
}

// fetch downloads a link without any session, as an API client or <img> tag would.
func fetch(t *testing.T, link string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), b
}

func pngBytes(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMediaUploadAndSend(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	h.inbound(customer, "wamid.IN1", "Send me the menu")

	pic := pngBytes(t)
	var m media.Media
	c.upload(phone.ID, "menu.png", "image/png", pic, http.StatusCreated, &m)
	if m.MimeType != "image/png" || m.SizeBytes != int64(len(pic)) || m.Filename == nil || *m.Filename != "menu.png" {
		t.Fatalf("uploaded = %+v", m)
	}
	var got media.Media
	c.do("GET", "/v1/media/"+m.ID.String(), nil, http.StatusOK, &got)
	status, ctype, body := fetch(t, got.URL)
	if status != http.StatusOK || ctype != "image/png" || !bytes.Equal(body, pic) {
		t.Fatalf("download = %d %s %d bytes", status, ctype, len(body))
	}
	if status, _, _ := fetch(t, strings.Replace(got.URL, "sig=", "sig=0", 1)); status != http.StatusForbidden {
		t.Fatalf("tampered link = %d, want 403", status)
	}

	// Content that does not match the declared type, unsupported types and oversize files are refused.
	var e apiErr
	c.upload(phone.ID, "fake.png", "image/png", []byte("not a picture"), http.StatusBadRequest, nil)
	c.upload(phone.ID, "app.exe", "application/x-msdownload", []byte("MZ"), http.StatusBadRequest, nil)
	sticker := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 600<<10)...)
	c.upload(phone.ID, "big.webp", "image/webp", sticker, http.StatusRequestEntityTooLarge, &e)
	if e.Error.Code != "media_too_large" {
		t.Fatalf("oversize sticker error = %+v", e.Error)
	}
	// A document type is taken from the file name when the client sends no type.
	var doc media.Media
	c.upload(phone.ID, "invoice.pdf", "", []byte("%PDF-1.4 test"), http.StatusCreated, &doc)
	if doc.MimeType != "application/pdf" {
		t.Fatalf("pdf mime = %s", doc.MimeType)
	}

	// Sending the image uploads it to Meta once and sends Meta's media ID.
	imageMsg := map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "image",
		"image": map[string]any{"media_id": m.ID, "caption": "Today's menu"}}
	var msg messaging.Message
	c.send(imageMsg, "", http.StatusAccepted, &msg)
	if msg.MediaID == nil || *msg.MediaID != m.ID {
		t.Fatalf("queued message media = %v", msg.MediaID)
	}
	if err := h.runSend(me.Tenant.ID, msg.ID, 1); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := h.meta.sent[len(h.meta.sent)-1]
	img, _ := sent["image"].(map[string]any)
	if img["id"] != "metamedia-1" || img["media_id"] != nil || img["caption"] != "Today's menu" || len(h.meta.uploads) != 1 ||
		!bytes.Equal(h.meta.uploads[0], pic) {
		t.Fatalf("Meta got %v after %d uploads", sent, len(h.meta.uploads))
	}
	c.send(imageMsg, "", http.StatusAccepted, &msg)
	if err := h.runSend(me.Tenant.ID, msg.ID, 1); err != nil {
		t.Fatal(err)
	}
	if img, _ := h.meta.sent[len(h.meta.sent)-1]["image"].(map[string]any); img["id"] != "metamedia-1" || len(h.meta.uploads) != 1 {
		t.Fatalf("second send re-uploaded: %v, %d uploads", img, len(h.meta.uploads))
	}

	// A PNG cannot go out as a video; any file can go as a document.
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "video",
		"video": map[string]any{"media_id": m.ID}}, "", http.StatusUnprocessableEntity, nil)
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "document",
		"document": map[string]any{"media_id": doc.ID, "filename": "invoice.pdf"}}, "", http.StatusAccepted, nil)
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "image",
		"image": map[string]any{"media_id": uuid.New()}}, "", http.StatusUnprocessableEntity, nil)

	// Another workspace can neither see nor send the file.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", "/v1/media/"+m.ID.String(), nil, http.StatusNotFound, nil)
}

func TestInboundMediaIsCopied(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	h.meta.inbound["media-777"] = []byte("\xff\xd8\xff\xe0 jpeg bytes")

	err := h.webhook("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":"555001"},
		"contacts":[{"wa_id":%q,"profile":{"name":"Ravi"}}],
		"messages":[{"from":%q,"id":"wamid.PIC","timestamp":"%d","type":"image","image":{"id":"media-777","mime_type":"image/jpeg","caption":"Is this one in stock?"}}]}`,
		customer, customer, time.Now().Unix()))
	if err != nil {
		t.Fatal(err)
	}

	var args media.DownloadArgs
	err = h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(context.Background(), "SELECT args FROM river_job WHERE kind = 'download_media'").Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &args)
	})
	if err != nil || args.MetaMediaID != "media-777" {
		t.Fatalf("download job: %v %+v", err, args)
	}
	run := func() error {
		return h.downloader.Work(context.Background(), &river.Job[media.DownloadArgs]{
			JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 10}, Args: args,
		})
	}
	if err := run(); err != nil {
		t.Fatalf("download: %v", err)
	}
	if err := run(); err != nil { // a retry after success changes nothing
		t.Fatal(err)
	}

	var msg messaging.Message
	c.do("GET", "/v1/messages/"+args.MessageID.String(), nil, http.StatusOK, &msg)
	if msg.MediaID == nil {
		t.Fatal("message has no media after download")
	}
	var m media.Media
	c.do("GET", "/v1/media/"+msg.MediaID.String(), nil, http.StatusOK, &m)
	status, ctype, body := fetch(t, m.URL)
	if status != http.StatusOK || ctype != "image/jpeg" || !bytes.Equal(body, h.meta.inbound["media-777"]) {
		t.Fatalf("download = %d %s %q", status, ctype, body)
	}
	if h.meta.called("GET /media-777") != 1 {
		t.Fatalf("media lookups = %d, want 1", h.meta.called("GET /media-777"))
	}

	// Media Meta no longer has is given up on, not retried forever.
	args.MetaMediaID, args.MessageID = "media-gone", uuid.New()
	if err := run(); err == nil {
		t.Fatal("download of a missing message succeeded")
	}
}
