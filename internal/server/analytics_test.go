package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

func TestAnalytics(t *testing.T) {
	h := newHarness(t)
	h.meta.templates = []string{`{"id":"8201","name":"offer","language":"en","status":"APPROVED","category":"MARKETING",
		"components":[{"type":"BODY","text":"Sale today"}]}`}
	c, me, phone := h.connected()

	// The customer writes, gets a free reply and a paid marketing template that they read.
	h.inbound(customer, "wamid.IN1", "Hi")
	var reply, promo messaging.Message
	c.send(text(phone.ID, "Hello!"), "", http.StatusAccepted, &reply)
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "template",
		"template": map[string]any{"name": "offer", "language": "en"}}, "", http.StatusAccepted, &promo)
	for _, m := range []*messaging.Message{&reply, &promo} {
		if err := h.runSend(me.Tenant.ID, m.ID, 1); err != nil {
			t.Fatal(err)
		}
		c.do("GET", "/v1/messages/"+m.ID.String(), nil, http.StatusOK, m)
	}
	status := func(wamid, st, category string, billable bool) {
		t.Helper()
		err := h.webhook("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
			"metadata":{"display_phone_number":"919876543210","phone_number_id":"555001"},
			"statuses":[{"id":%q,"status":%q,"timestamp":"%d","recipient_id":%q,
			  "pricing":{"billable":%t,"category":%q,"pricing_model":"PMP"}}]}`, wamid, st, time.Now().Unix(), customer, billable, category))
		if err != nil {
			t.Fatal(err)
		}
	}
	status(*reply.Wamid, "delivered", "service", false)
	status(*promo.Wamid, "read", "marketing", true)

	var rep analytics.Report
	c.do("GET", "/internal/analytics", nil, http.StatusOK, &rep)
	want := analytics.Counts{Sent: 2, Delivered: 2, Read: 1, Received: 1, Billable: 1, EstCostMinor: 78}
	if rep.Totals != want || len(rep.Days) != 30 || rep.TimeZone != "Asia/Kolkata" || rep.Currency != "INR" {
		t.Fatalf("report = %+v", rep)
	}
	if last := rep.Days[29]; last.Sent != 2 || last.Received != 1 {
		t.Fatalf("today = %+v", last)
	}
	byKey := func(gs []analytics.Group) map[string]analytics.Counts {
		m := map[string]analytics.Counts{}
		for _, g := range gs {
			m[g.Key] = g.Counts
		}
		return m
	}
	if cat := byKey(rep.ByCategory); cat["marketing"].Billable != 1 || cat["service"].Sent != 1 || cat["service"].Received != 1 {
		t.Fatalf("by category = %+v", rep.ByCategory)
	}
	if o := byKey(rep.ByOrigin); o["dashboard"].Sent != 2 || o["customer"].Received != 1 {
		t.Fatalf("by origin = %+v", rep.ByOrigin)
	}
	if len(rep.ByCountry) != 1 || rep.ByCountry[0].Key != "IN" || len(rep.ByNumber) != 1 || rep.ByNumber[0].Key != phone.ID.String() {
		t.Fatalf("by country %+v, by number %+v", rep.ByCountry, rep.ByNumber)
	}

	// The hourly job rolls up every tenant; a range ending yesterday leaves today out.
	if err := analytics.NewWorker(h.db, h.log).Work(context.Background(), &river.Job[analytics.RollupArgs]{JobRow: &rivertype.JobRow{}}); err != nil {
		t.Fatal(err)
	}
	ist := time.FixedZone("IST", 5*3600+1800)
	yesterday := time.Now().In(ist).AddDate(0, 0, -1)
	from := yesterday.AddDate(0, 0, -9).Format(time.DateOnly)
	c.do("GET", "/internal/analytics?from="+from+"&to="+yesterday.Format(time.DateOnly), nil, http.StatusOK, &rep)
	if rep.Totals.Sent != 0 || rep.From != from || len(rep.Days) != 10 {
		t.Fatalf("past range = %+v", rep.Totals)
	}
	c.do("GET", "/internal/analytics?from=2024-01-01&to=2026-01-01", nil, http.StatusBadRequest, nil)
	c.do("GET", "/internal/analytics?from=2026-02-01&to=2026-01-01", nil, http.StatusBadRequest, nil)
	c.do("GET", "/internal/analytics?phone_number_id=nope", nil, http.StatusBadRequest, nil)
}
