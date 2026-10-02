package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/campaigns"
	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

// createCampaign posts to /v1/campaigns with an optional Idempotency-Key.
func (c *client) createCampaign(body any, key string, want int, out any) http.Header {
	c.h.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.h.api.URL+"/v1/campaigns", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
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
		c.h.t.Fatalf("POST /v1/campaigns = %d, want %d: %s", resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.h.t.Fatalf("decode: %v: %s", err, raw)
		}
	}
	return resp.Header
}

func (h *harness) runCampaign(tenantID, id uuid.UUID) time.Time {
	h.t.Helper()
	next, err := h.campaigns.Run(context.Background(), campaigns.RunArgs{CampaignID: id, TenantID: tenantID})
	if err != nil {
		h.t.Fatalf("run campaign: %v", err)
	}
	return next
}

type recipientPage struct {
	Data       []campaigns.Recipient `json:"data"`
	NextCursor *string               `json:"next_cursor"`
}

func TestCampaigns(t *testing.T) {
	h := newHarness(t)
	h.meta.templates = []string{
		`{"id":"8101","name":"festival_offer","language":"en","status":"APPROVED","category":"MARKETING",
		  "components":[{"type":"BODY","text":"Hi {{1}}, enjoy {{2}} off today!"},
		                {"type":"BUTTONS","buttons":[{"type":"URL","text":"Shop","url":"https://shop.example/{{1}}"}]}]}`,
		`{"id":"8102","name":"old_offer","language":"en","status":"PAUSED","category":"MARKETING",
		  "components":[{"type":"BODY","text":"Sale"}]}`,
	}
	c, me, phone := h.connected()

	contact := func(waID, name, consent string, blocked bool, fields map[string]any, tags ...string) uuid.UUID {
		body := map[string]any{"wa_id": waID, "tags": tags, "custom_fields": fields}
		if name != "" {
			body["name"] = name
		}
		if consent != "" {
			body["opt_in"] = map[string]any{"status": consent, "source": "dashboard"}
		}
		var ct contacts.Contact
		c.do("POST", "/v1/contacts", body, http.StatusCreated, &ct)
		if blocked {
			c.do("PATCH", "/v1/contacts/"+ct.ID.String(), map[string]any{"blocked": true}, http.StatusOK, nil)
		}
		return ct.ID
	}
	meera := contact("919800000011", "Meera Nair", "opted_in", false, map[string]any{"discount": "20%"}, "diwali")
	contact("919800000012", "", "opted_in", false, map[string]any{"discount": 15}, "diwali")
	contact("919800000013", "Opted Out", "opted_out", false, nil, "diwali")
	contact("919800000014", "Blocked", "opted_in", true, map[string]any{"discount": "5%"}, "diwali")
	contact("919800000015", "Unknown", "", false, nil, "Diwali")
	contact("919800000016", "No Discount", "opted_in", false, nil, "diwali")
	farah := contact("919800000017", "Farah", "opted_in", false, map[string]any{"discount": "10%"})

	audience := map[string]any{"tags": []string{"DIWALI"}, "contact_ids": []uuid.UUID{farah}}
	var counts campaigns.AudienceCounts
	c.do("POST", "/internal/campaigns/audience", audience, http.StatusOK, &counts)
	if counts != (campaigns.AudienceCounts{Total: 7, Eligible: 4, Blocked: 1, OptedOut: 1, NoOptIn: 1}) {
		t.Fatalf("audience = %+v", counts)
	}

	vars := map[string]string{"1": "{{contact.first_name|there}}", "2": "{{contact.discount}}", "button.0": "{{contact.wa_id}}"}
	req := func(mod func(map[string]any)) map[string]any {
		b := map[string]any{
			"name": "Diwali offer", "phone_number_id": phone.ID, "audience": audience,
			"template": map[string]any{"name": "festival_offer", "language": "en", "variables": vars},
		}
		if mod != nil {
			mod(b)
		}
		return b
	}
	var e apiErr
	for _, bad := range []struct {
		mod    func(map[string]any)
		status int
		code   string
	}{
		{func(b map[string]any) { b["template"] = map[string]any{"name": "festival_offer", "language": "en"} }, 422, "template_param_mismatch"},
		{func(b map[string]any) {
			b["template"] = map[string]any{"name": "festival_offer", "language": "en", "variables": map[string]string{"1": "a", "2": "b", "button.0": "c", "3": "d"}}
		}, 400, "invalid_request"},
		{func(b map[string]any) {
			b["template"] = map[string]any{"name": "festival_offer", "language": "en", "variables": map[string]string{"1": "{{name}}", "2": "b", "button.0": "c"}}
		}, 400, "invalid_request"},
		{func(b map[string]any) { b["template"] = map[string]any{"name": "old_offer", "language": "en"} }, 422, "template_not_approved"},
		{func(b map[string]any) { b["audience"] = map[string]any{"tags": []string{"nobody"}} }, 422, "audience_empty"},
		{func(b map[string]any) { b["audience"] = map[string]any{} }, 400, "invalid_request"},
		{func(b map[string]any) { b["send_rate_per_min"] = 0 }, 400, "invalid_request"},
		{func(b map[string]any) { b["scheduled_at"] = time.Now().Add(-time.Hour) }, 400, "invalid_request"},
	} {
		c.createCampaign(req(bad.mod), "", bad.status, &e)
		if e.Error.Code != bad.code {
			t.Fatalf("error = %+v, want %s", e.Error, bad.code)
		}
	}

	// Creating is idempotent with a key.
	var camp campaigns.Campaign
	c.createCampaign(req(nil), "diwali-1", http.StatusCreated, &camp)
	if camp.Status != "scheduled" || camp.Template.Name != "festival_offer" || camp.SendRatePerMin != campaigns.DefaultRate {
		t.Fatalf("created = %+v", camp)
	}
	var again campaigns.Campaign
	hdr := c.createCampaign(req(nil), "diwali-1", http.StatusCreated, &again)
	if again.ID != camp.ID || hdr.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %+v %v", again, hdr)
	}

	// One run expands the audience and queues everyone the 250-a-day limit allows.
	if next := h.runCampaign(me.Tenant.ID, camp.ID); !next.IsZero() {
		t.Fatalf("next run at %v, want none", next)
	}
	c.do("GET", "/v1/campaigns/"+camp.ID.String(), nil, http.StatusOK, &camp)
	if camp.Status != "completed" || camp.StartedAt == nil || camp.FinishedAt == nil ||
		camp.Stats != (campaigns.Stats{Total: 7, Skipped: 4, Queued: 3}) {
		t.Fatalf("after run = %+v", camp)
	}
	var skipped recipientPage
	c.do("GET", "/internal/campaigns/"+camp.ID.String()+"/recipients?status=skipped", nil, http.StatusOK, &skipped)
	reasons := map[string]string{}
	for _, r := range skipped.Data {
		reasons[r.WaID] = *r.SkipReason
	}
	want := map[string]string{"919800000013": "opted_out", "919800000014": "blocked", "919800000015": "no_opt_in", "919800000016": "missing_variable"}
	if len(reasons) != len(want) {
		t.Fatalf("skipped = %v", reasons)
	}
	for k, v := range want {
		if reasons[k] != v {
			t.Fatalf("skipped = %v, want %v", reasons, want)
		}
	}

	// The send worker sends each message with the contact's values filled in.
	var queued recipientPage
	c.do("GET", "/internal/campaigns/"+camp.ID.String()+"/recipients?status=queued&limit=2", nil, http.StatusOK, &queued)
	if len(queued.Data) != 2 || queued.NextCursor == nil {
		t.Fatalf("queued page = %+v", queued)
	}
	var rest recipientPage
	c.do("GET", "/internal/campaigns/"+camp.ID.String()+"/recipients?status=queued&limit=2&cursor="+*queued.NextCursor, nil, http.StatusOK, &rest)
	all := append(queued.Data, rest.Data...)
	if len(all) != 3 {
		t.Fatalf("queued = %+v", all)
	}
	var meeraMsg uuid.UUID
	for _, r := range all {
		if err := h.runSend(me.Tenant.ID, *r.MessageID, 1); err != nil {
			t.Fatal(err)
		}
		if r.ContactID == meera {
			meeraMsg = *r.MessageID
		}
	}
	bodies := map[string]string{}
	for _, s := range h.meta.sent {
		tpl := s["template"].(map[string]any)
		comps := tpl["components"].([]any)
		body := comps[0].(map[string]any)["parameters"].([]any)
		btn := comps[1].(map[string]any)
		bodies[s["to"].(string)] = body[0].(map[string]any)["text"].(string) + "|" + body[1].(map[string]any)["text"].(string) +
			"|" + btn["parameters"].([]any)[0].(map[string]any)["text"].(string)
	}
	if bodies["919800000011"] != "Meera|20%|919800000011" || bodies["919800000012"] != "there|15|919800000012" ||
		bodies["919800000017"] != "Farah|10%|919800000017" {
		t.Fatalf("sent = %v", bodies)
	}

	// Delivery statuses reach the report.
	var m messaging.Message
	c.do("GET", "/v1/messages/"+meeraMsg.String(), nil, http.StatusOK, &m)
	if m.Origin != "campaign" || m.CampaignID == nil || *m.CampaignID != camp.ID {
		t.Fatalf("campaign message = %+v", m)
	}
	if err := h.status(*m.Wamid, "read"); err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/v1/campaigns/"+camp.ID.String(), nil, http.StatusOK, &camp)
	if camp.Stats != (campaigns.Stats{Total: 7, Skipped: 4, Sent: 3, Delivered: 1, Read: 1}) {
		t.Fatalf("stats = %+v", camp.Stats)
	}
	c.do("POST", "/v1/campaigns/"+camp.ID.String()+"/cancel", nil, http.StatusConflict, nil)

	// A slow campaign sends in batches at its rate.
	var slow campaigns.Campaign
	c.createCampaign(req(func(b map[string]any) {
		b["send_rate_per_min"] = 1
		b["audience"] = map[string]any{"contact_ids": []uuid.UUID{meera, farah}}
	}), "", http.StatusCreated, &slow)
	next := h.runCampaign(me.Tenant.ID, slow.ID)
	if d := time.Until(next); d < 50*time.Second || d > 70*time.Second {
		t.Fatalf("next batch in %v, want a minute", d)
	}
	c.do("GET", "/v1/campaigns/"+slow.ID.String(), nil, http.StatusOK, &slow)
	if slow.Status != "running" || slow.Stats.Queued != 1 || slow.Stats.Pending != 1 {
		t.Fatalf("slow after first batch = %+v", slow)
	}
	if next := h.runCampaign(me.Tenant.ID, slow.ID); !next.IsZero() {
		t.Fatal("slow campaign should finish on its second batch")
	}

	// A scheduled campaign can be cancelled before it starts, and its run job then does nothing.
	var later campaigns.Campaign
	c.createCampaign(req(func(b map[string]any) { b["scheduled_at"] = time.Now().Add(time.Hour) }), "", http.StatusCreated, &later)
	c.do("POST", "/v1/campaigns/"+later.ID.String()+"/cancel", nil, http.StatusOK, &later)
	if later.Status != "cancelled" || later.FinishedAt == nil {
		t.Fatalf("cancelled = %+v", later)
	}
	if next := h.runCampaign(me.Tenant.ID, later.ID); !next.IsZero() {
		t.Fatal("cancelled campaign ran")
	}
	c.do("GET", "/v1/campaigns/"+later.ID.String(), nil, http.StatusOK, &later)
	if later.Status != "cancelled" || later.Stats.Total != 0 {
		t.Fatalf("cancelled after run = %+v", later)
	}

	var list struct {
		Data       []campaigns.Campaign `json:"data"`
		NextCursor *string              `json:"next_cursor"`
	}
	c.do("GET", "/v1/campaigns?limit=2", nil, http.StatusOK, &list)
	if len(list.Data) != 2 || list.Data[0].ID != later.ID || list.NextCursor == nil {
		t.Fatalf("list = %+v", list)
	}
	c.do("GET", "/v1/campaigns?limit=2&cursor="+*list.NextCursor, nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.Data[0].ID != camp.ID || list.Data[0].Stats.Read != 1 {
		t.Fatalf("list page 2 = %+v", list)
	}
}
