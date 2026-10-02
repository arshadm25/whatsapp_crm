package server_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
)

// A 1x1 PNG.
var tinyPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func (c *client) uploadFile(path, field, filename string, data []byte, wantStatus int) {
	c.h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, filename)
	_, _ = fw.Write(data)
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
	if resp.StatusCode != wantStatus {
		c.h.t.Fatalf("POST %s = %d, want %d: %s", path, resp.StatusCode, wantStatus, raw)
	}
}

func TestBusinessProfile(t *testing.T) {
	h := newHarness(t)
	c, _, n := h.connected()
	base := "/v1/phone-numbers/" + n.ID.String() + "/profile"
	h.meta.profile = map[string]any{"about": "Hello", "websites": []string{}}

	var prof struct {
		About             string   `json:"about"`
		Email             string   `json:"email"`
		Websites          []string `json:"websites"`
		ProfilePictureURL string   `json:"profile_picture_url"`
	}
	c.do("GET", base, nil, http.StatusOK, &prof)
	if prof.About != "Hello" {
		t.Fatalf("profile = %+v", prof)
	}

	c.do("PATCH", base, map[string]any{"about": "Sweets since 1962", "email": "hi@sharma.example", "websites": []string{"https://sharma.example"}, "vertical": "RETAIL"}, http.StatusOK, &prof)
	if prof.About != "Sweets since 1962" || prof.Email != "hi@sharma.example" || len(prof.Websites) != 1 {
		t.Fatalf("updated = %+v", prof)
	}

	var e apiErr
	for _, bad := range []map[string]any{
		{}, {"about": ""}, {"email": "nope"}, {"websites": []string{"ftp://x"}}, {"vertical": "SPACE"},
		{"websites": []string{"https://a.example", "https://b.example", "https://c.example"}},
	} {
		c.do("PATCH", base, bad, http.StatusBadRequest, &e)
	}

	c.uploadFile(base+"/logo", "file", "logo.png", tinyPNG, http.StatusOK)
	c.do("GET", base, nil, http.StatusOK, &prof)
	if prof.ProfilePictureURL == "" || len(h.meta.pictures) != 1 {
		t.Fatalf("picture = %q, uploads = %d", prof.ProfilePictureURL, len(h.meta.pictures))
	}
	c.uploadFile(base+"/logo", "file", "notes.txt", []byte("not a picture"), http.StatusBadRequest)

	// Another workspace cannot see it.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", base, nil, http.StatusNotFound, nil)
	other.do("PATCH", base, map[string]any{"about": "x"}, http.StatusNotFound, nil)
}

func TestDisconnectAndReconnectNumber(t *testing.T) {
	h := newHarness(t)
	owner, me, n := h.connected()
	staff := h.platformAdmin("ops@ecogo.co.in")
	staff.do("PUT", "/internal/admin/plans/solo", map[string]any{"name": "solo", "price_minor": 100000, "included_numbers": 1,
		"included_seats": 5, "extra_seat_minor": 10000, "is_active": true, "razorpay_plan_id": "plan_solo"}, http.StatusOK, nil)
	owner.do("POST", "/internal/billing/subscribe", map[string]string{"plan": "solo"}, http.StatusOK, nil)
	h.razorpayEvent("evt_a", "subscription.authenticated", map[string]any{"id": "sub_1", "plan_id": "plan_solo", "status": "authenticated"}, "whsec")

	// The only slot is taken, so a second number is refused.
	var e apiErr
	owner.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusConflict, &e)
	if e.Error.Code != "plan_limit" {
		t.Fatalf("before disconnect = %+v", e)
	}

	owner.do("POST", "/internal/numbers/"+n.ID.String()+"/disconnect", nil, http.StatusOK, &n)
	if n.Status != "disconnected" || h.meta.called("POST /555001/deregister") != 1 {
		t.Fatalf("number = %+v, deregister calls = %d", n, h.meta.called("POST /555001/deregister"))
	}
	owner.do("POST", "/internal/numbers/"+n.ID.String()+"/disconnect", nil, http.StatusConflict, &e)
	if e.Error.Code != "already_disconnected" {
		t.Fatalf("second disconnect = %+v", e)
	}

	// A disconnected number has no profile to edit, and its slot is free.
	owner.do("PATCH", "/v1/phone-numbers/"+n.ID.String()+"/profile", map[string]any{"about": "x"}, http.StatusUnprocessableEntity, nil)
	owner.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusCreated, nil)

	// Connecting the same number again registers it afresh.
	again := h.connect(owner, me.Tenant.ID, "555001")
	if err := h.runJob(me.Tenant.ID, again.ID, 1); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	var got numbers.PhoneNumber
	owner.do("GET", "/v1/phone-numbers/"+n.ID.String(), nil, http.StatusOK, &got)
	if got.Status != "connected" || h.meta.called("POST /555001/register") != 2 {
		t.Fatalf("after reconnect: %+v, registers = %d", got, h.meta.called("POST /555001/register"))
	}
}
