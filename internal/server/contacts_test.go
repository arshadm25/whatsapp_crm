package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

func (c *client) importCSV(csv string, confirmed bool, want int, out any) {
	c.h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "customers.csv")
	_, _ = part.Write([]byte(csv))
	if confirmed {
		_ = mw.WriteField("consent_confirmed", "true")
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.h.api.URL+"/internal/contacts/import", &buf)
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
		c.h.t.Fatalf("import = %d, want %d: %s", resp.StatusCode, want, raw)
	}
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
}

// fieldsOf renders custom fields as k=v pairs in key order.
func fieldsOf(c contacts.Contact) string {
	var m map[string]string
	_ = json.Unmarshal(c.CustomFields, &m)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return strings.Join(out, ",")
}

type contactPage struct {
	Data       []contacts.Contact
	NextCursor *string `json:"next_cursor"`
}

func TestContacts(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()

	var ct contacts.Contact
	c.do("POST", "/v1/contacts", map[string]any{
		"wa_id": "919800000002", "name": "Meera", "language": "ml", "tags": []string{"vip", "Diwali", "VIP"},
		"custom_fields": map[string]any{"city": "Kochi"},
		"opt_in":        map[string]any{"status": "opted_in", "source": "dashboard", "evidence": "Shop counter form"},
	}, http.StatusCreated, &ct)
	if ct.OptInStatus != "opted_in" || ct.OptedInAt == nil || len(ct.Tags) != 2 || fieldsOf(ct) != `city=Kochi` {
		t.Fatalf("created = %+v %s", ct, ct.CustomFields)
	}
	// Posting the same number updates it; a null custom field removes it.
	c.do("POST", "/v1/contacts", map[string]any{
		"wa_id": "919800000002", "tags": []string{"vip"}, "custom_fields": map[string]any{"city": nil, "tier": "gold"},
	}, http.StatusOK, &ct)
	if *ct.Name != "Meera" || len(ct.Tags) != 2 || fieldsOf(ct) != `tier=gold` {
		t.Fatalf("updated = %+v %s", ct, ct.CustomFields)
	}
	for _, bad := range []map[string]any{
		{"wa_id": "98765"},
		{"wa_id": "919800000003", "language": "Hindi"},
		{"wa_id": "919800000003", "opt_in": map[string]any{"status": "opted_in", "source": "keyword"}},
		{"wa_id": "919800000003", "custom_fields": []int{1}},
	} {
		c.do("POST", "/v1/contacts", bad, http.StatusBadRequest, nil)
	}
	c.do("POST", "/v1/contacts", map[string]any{"wa_id": "919800000004", "name": "Arjun"}, http.StatusCreated, nil)

	var page contactPage
	c.do("GET", "/v1/contacts?tag=VIP", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].ID != ct.ID {
		t.Fatalf("by tag = %+v", page.Data)
	}
	c.do("GET", "/v1/contacts?opt_in_status=opted_in", nil, http.StatusOK, &page)
	if len(page.Data) != 1 {
		t.Fatalf("opted in = %d", len(page.Data))
	}
	c.do("GET", "/v1/contacts?q=arj", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || *page.Data[0].Name != "Arjun" {
		t.Fatalf("search = %+v", page.Data)
	}
	c.do("GET", "/v1/contacts?limit=1", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.NextCursor == nil {
		t.Fatalf("page 1 = %+v", page)
	}
	first := page.Data[0].ID
	c.do("GET", "/v1/contacts?limit=1&cursor="+*page.NextCursor, nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].ID == first {
		t.Fatalf("page 2 = %+v", page)
	}

	// PATCH edits tags, blocking and consent; consent history keeps every change.
	c.do("PATCH", "/v1/contacts/"+ct.ID.String(), map[string]any{
		"remove_tags": []string{"diwali"}, "add_tags": []string{"wholesale"}, "blocked": true, "name": "",
		"consent": map[string]any{"status": "opted_out", "source": "dashboard"},
	}, http.StatusOK, &ct)
	if ct.Name != nil || !ct.Blocked || ct.OptInStatus != "opted_out" || len(ct.Tags) != 2 || ct.Tags[0] != "vip" || ct.Tags[1] != "wholesale" {
		t.Fatalf("patched = %+v", ct)
	}
	var hist struct{ Data []contacts.ConsentEvent }
	c.do("GET", "/internal/contacts/"+ct.ID.String()+"/consent", nil, http.StatusOK, &hist)
	if len(hist.Data) != 2 || hist.Data[0].Kind != "opt_out" || hist.Data[1].Evidence == nil || hist.Data[0].RecordedByName == nil {
		t.Fatalf("history = %+v", hist.Data)
	}
	var tags struct{ Data []contacts.Tag }
	c.do("GET", "/internal/contacts/tags", nil, http.StatusOK, &tags)
	if len(tags.Data) != 3 {
		t.Fatalf("tags = %+v", tags.Data)
	}

	// Consent records cannot be changed by the app.
	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE consent_events SET kind = 'opt_in'")
		return err
	})
	if err == nil {
		t.Fatal("consent_events was updated")
	}

	// CSV import: national numbers get the country code; opt-in needs the uploader's confirmation.
	csv := "Phone,Name,Tags,Opt-in,City\n" +
		"98765 43210,Asha,vip;new,yes,Kochi\n" +
		"+44 7700 900123,Tom,,no,London\n" +
		"abc,Bad,,,\n" +
		"09876543211,Ravi,,yes,\n"
	var res contacts.ImportResult
	c.importCSV(csv, false, http.StatusOK, &res)
	if res.Rows != 4 || res.Created != 3 || res.Skipped != 1 || res.OptedIn != 0 || res.OptedOut != 1 ||
		len(res.Errors) != 1 || res.Errors[0].Line != 4 {
		t.Fatalf("import = %+v", res)
	}
	c.importCSV(csv, true, http.StatusOK, &res)
	if res.Updated != 3 || res.OptedIn != 2 || res.OptedOut != 0 {
		t.Fatalf("second import = %+v", res)
	}
	c.do("GET", "/v1/contacts?q=919876543210", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].OptInStatus != "opted_in" || len(page.Data[0].Tags) != 2 ||
		fieldsOf(page.Data[0]) != `City=Kochi` {
		t.Fatalf("imported contact = %+v %s", page.Data, page.Data[0].CustomFields)
	}
	c.importCSV("Name\nAsha\n", true, http.StatusBadRequest, nil)

	// A customer replying STOP opts out; sends are then refused. START opts back in.
	h.inbound(customer, "wamid.S1", "Stop")
	var e apiErr
	c.send(text(phone.ID, "Sale today"), "", http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "contact_opted_out" {
		t.Fatalf("send after STOP = %+v", e.Error)
	}
	h.inbound(customer, "wamid.S2", "START")
	c.send(text(phone.ID, "Welcome back"), "", http.StatusAccepted, nil)

	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", "/v1/contacts/"+ct.ID.String(), nil, http.StatusNotFound, nil)
	other.do("PATCH", "/v1/contacts/"+ct.ID.String(), map[string]any{"blocked": false}, http.StatusNotFound, nil)
}
