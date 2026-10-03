package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/campaigns"
	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// runCampaignNow runs a campaign step with the generation its latest job carries.
func (h *harness) runCampaignNow(tenantID, id uuid.UUID) time.Time {
	h.t.Helper()
	var gen int32
	err := h.db.InTenant(context.Background(), tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		c, err := q.GetCampaign(context.Background(), id)
		gen = c.RunGeneration
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	next, err := h.campaigns.Run(context.Background(), campaigns.RunArgs{CampaignID: id, TenantID: tenantID, Generation: gen})
	if err != nil {
		h.t.Fatalf("run campaign: %v", err)
	}
	return next
}

func TestCampaignDraftsEditsAndPause(t *testing.T) {
	h := newHarness(t)
	h.meta.templates = []string{
		`{"id":"8201","name":"promo","language":"en","status":"APPROVED","category":"MARKETING",
		  "components":[{"type":"BODY","text":"Big sale today"}]}`,
		`{"id":"8202","name":"pending_promo","language":"en","status":"PENDING","category":"MARKETING",
		  "components":[{"type":"BODY","text":"Coming soon"}]}`,
	}
	c, me, phone := h.connected()
	for _, wa := range []string{"919800000021", "919800000022", "919800000023"} {
		c.do("POST", "/v1/contacts", map[string]any{"wa_id": wa, "tags": []string{"vip"},
			"opt_in": map[string]any{"status": "opted_in", "source": "dashboard"}}, http.StatusCreated, nil)
	}
	var ct contacts.Contact
	c.do("POST", "/v1/contacts", map[string]any{"wa_id": "919800000024"}, http.StatusCreated, &ct)

	// A draft may use a template that is not approved yet and have no audience.
	draft := map[string]any{
		"name": "Weekend", "phone_number_id": phone.ID, "draft": true, "audience": map[string]any{},
		"template": map[string]any{"name": "pending_promo", "language": "en"},
	}
	var camp campaigns.Campaign
	c.createCampaign(draft, "", http.StatusCreated, &camp)
	if camp.Status != "draft" {
		t.Fatalf("draft = %+v", camp)
	}
	if next := h.runCampaignNow(me.Tenant.ID, camp.ID); !next.IsZero() {
		t.Fatal("a draft ran")
	}
	draft["template"] = map[string]any{"name": "nope", "language": "en"}
	var e apiErr
	c.do("PUT", "/v1/campaigns/"+camp.ID.String(), draft, http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "template_not_found" {
		t.Fatalf("error = %+v", e.Error)
	}

	// Sending the draft runs the full checks.
	send := map[string]any{
		"name": "Weekend", "phone_number_id": phone.ID, "audience": map[string]any{"tags": []string{"vip"}},
		"template": map[string]any{"name": "pending_promo", "language": "en"}, "send_rate_per_min": 1,
		"scheduled_at": time.Now().Add(time.Hour),
	}
	c.do("PUT", "/v1/campaigns/"+camp.ID.String(), send, http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "template_not_approved" {
		t.Fatalf("error = %+v", e.Error)
	}
	send["template"] = map[string]any{"name": "promo", "language": "en"}
	c.do("PUT", "/v1/campaigns/"+camp.ID.String(), send, http.StatusOK, &camp)
	if camp.Status != "scheduled" || camp.ScheduledAt == nil || camp.SendRatePerMin != 1 {
		t.Fatalf("scheduled = %+v", camp)
	}

	// The job queued before an edit does nothing; the latest one starts the campaign.
	if _, err := h.campaigns.Run(context.Background(), campaigns.RunArgs{CampaignID: camp.ID, TenantID: me.Tenant.ID}); err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/v1/campaigns/"+camp.ID.String(), nil, http.StatusOK, &camp)
	if camp.Status != "scheduled" {
		t.Fatalf("stale job started it: %+v", camp)
	}
	if next := h.runCampaignNow(me.Tenant.ID, camp.ID); next.IsZero() {
		t.Fatal("want another batch")
	}
	c.do("GET", "/v1/campaigns/"+camp.ID.String(), nil, http.StatusOK, &camp)
	if camp.Status != "running" || camp.Stats.Queued != 1 || camp.Stats.Pending != 2 {
		t.Fatalf("running = %+v", camp)
	}
	c.do("PUT", "/v1/campaigns/"+camp.ID.String(), send, http.StatusConflict, nil)
	c.do("POST", "/v1/campaigns/"+camp.ID.String()+"/resume", nil, http.StatusConflict, nil)

	// Pause, and the next batch does not run.
	c.do("POST", "/v1/campaigns/"+camp.ID.String()+"/pause", nil, http.StatusOK, &camp)
	if camp.Status != "paused" {
		t.Fatalf("paused = %+v", camp)
	}
	if next := h.runCampaignNow(me.Tenant.ID, camp.ID); !next.IsZero() {
		t.Fatal("a paused campaign ran")
	}
	c.do("POST", "/v1/campaigns/"+camp.ID.String()+"/resume", nil, http.StatusOK, &camp)
	if camp.Status != "running" {
		t.Fatalf("resumed = %+v", camp)
	}
	h.runCampaignNow(me.Tenant.ID, camp.ID)
	c.do("GET", "/v1/campaigns/"+camp.ID.String(), nil, http.StatusOK, &camp)
	if camp.Stats.Queued != 2 {
		t.Fatalf("after resume = %+v", camp.Stats)
	}

	// Status filter and counts.
	var other campaigns.Campaign
	c.createCampaign(draft2(phone.ID, ct.ID), "", http.StatusCreated, &other)
	var page struct{ Data []campaigns.Campaign }
	c.do("GET", "/v1/campaigns?status=draft", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].ID != other.ID {
		t.Fatalf("drafts = %+v", page.Data)
	}
	c.do("GET", "/v1/campaigns?status=running,paused", nil, http.StatusOK, &page)
	if len(page.Data) != 1 || page.Data[0].ID != camp.ID {
		t.Fatalf("running = %+v", page.Data)
	}
	c.do("GET", "/v1/campaigns?status=sending", nil, http.StatusBadRequest, nil)
	var counts map[string]int
	c.do("GET", "/internal/campaigns/counts", nil, http.StatusOK, &counts)
	if counts["all"] != 2 || counts["draft"] != 1 || counts["running"] != 1 || counts["completed"] != 0 {
		t.Fatalf("counts = %v", counts)
	}

	// Drafts can be deleted; other campaigns cannot.
	c.do("DELETE", "/v1/campaigns/"+camp.ID.String(), nil, http.StatusConflict, nil)
	c.do("DELETE", "/v1/campaigns/"+other.ID.String(), nil, http.StatusNoContent, nil)
	c.do("GET", "/v1/campaigns/"+other.ID.String(), nil, http.StatusNotFound, nil)
}

func draft2(phone, contact uuid.UUID) map[string]any {
	return map[string]any{
		"name": "Later", "phone_number_id": phone, "draft": true, "audience": map[string]any{"contact_ids": []uuid.UUID{contact}},
		"template": map[string]any{"name": "promo", "language": "en"},
	}
}
