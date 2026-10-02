package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
)

// upload posts a multipart form with one file to an internal path.
func (c *client) uploadForm(path string, fields map[string]string, filename, mime string, data []byte, want int, out any) {
	c.h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	hdr.Set("Content-Type", mime)
	part, _ := mw.CreatePart(hdr)
	_, _ = part.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.h.api.URL+path, &buf)
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
		c.h.t.Fatalf("POST %s = %d, want %d: %s", path, resp.StatusCode, want, raw)
	}
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
}

func TestTemplateDraftsAndHeaderSamples(t *testing.T) {
	h := newHarness(t)
	c, _, phone := h.connected()
	acct := phone.WhatsappAccountID.String()

	// A header sample goes through Meta's resumable upload.
	var sample struct{ Handle, Format string }
	c.uploadForm("/internal/templates/header-samples", map[string]string{"whatsapp_account_id": acct}, "banner.png", "image/png",
		[]byte("\x89PNG fake"), http.StatusCreated, &sample)
	if sample.Handle != "4:handle" || sample.Format != "IMAGE" {
		t.Fatalf("sample = %+v", sample)
	}
	c.uploadForm("/internal/templates/header-samples", map[string]string{"whatsapp_account_id": acct}, "a.gif", "image/gif",
		[]byte("GIF"), http.StatusBadRequest, nil)

	body := map[string]any{
		"whatsapp_account_id": acct, "name": "new_arrivals", "language": "en", "category": "marketing", "draft": true,
		"components": []map[string]any{
			{"type": "HEADER", "format": "IMAGE", "example": map[string]any{"header_handle": []string{sample.Handle}}},
			{"type": "BODY", "text": "New arrivals are in!"},
		},
	}
	var tpl templates.Template
	c.do("POST", "/v1/templates", body, http.StatusCreated, &tpl)
	if tpl.Status != "draft" || tpl.MetaTemplateID != nil || h.meta.called("POST /"+phone.WabaID+"/message_templates") != 0 {
		t.Fatalf("draft = %+v", tpl)
	}
	c.do("POST", "/v1/templates", body, http.StatusConflict, nil)

	// Editing a draft stays local.
	c.do("PATCH", "/v1/templates/"+tpl.ID.String(), map[string]any{"components": []map[string]any{
		{"type": "BODY", "text": "Fresh stock is in!"},
	}}, http.StatusOK, &tpl)
	if tpl.Status != "draft" {
		t.Fatalf("edited draft = %+v", tpl)
	}
	var list struct{ Data []templates.Template }
	c.do("GET", "/v1/templates?status=draft", nil, http.StatusOK, &list)
	if len(list.Data) != 1 {
		t.Fatalf("drafts = %+v", list.Data)
	}

	// Submitting sends it to Meta for review.
	c.do("POST", "/v1/templates/"+tpl.ID.String()+"/submit", nil, http.StatusOK, &tpl)
	if tpl.Status != "pending" || tpl.MetaTemplateID == nil {
		t.Fatalf("submitted = %+v", tpl)
	}
	if last := h.meta.created[len(h.meta.created)-1]; last["name"] != "new_arrivals" {
		t.Fatalf("meta got %+v", last)
	}
	c.do("POST", "/v1/templates/"+tpl.ID.String()+"/submit", nil, http.StatusConflict, nil)
	c.do("DELETE", "/v1/templates/"+tpl.ID.String(), nil, http.StatusConflict, nil)

	// A draft can be deleted.
	body["name"] = "spare"
	c.do("POST", "/v1/templates", body, http.StatusCreated, &tpl)
	c.do("DELETE", "/v1/templates/"+tpl.ID.String(), nil, http.StatusNoContent, nil)
	c.do("GET", "/v1/templates/"+tpl.ID.String(), nil, http.StatusNotFound, nil)
}
