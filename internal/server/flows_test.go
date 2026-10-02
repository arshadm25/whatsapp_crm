package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/arshadm25/whatsapp_crm/internal/bots"
	"github.com/arshadm25/whatsapp_crm/internal/flows"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

// flowReply delivers a customer's Flow submission, as a reply to the Flow message wamid.
func (h *harness) flowReply(from, wamid, replyTo string, answers map[string]any) {
	h.t.Helper()
	b, _ := json.Marshal(answers)
	resp, _ := json.Marshal(map[string]any{"type": "interactive", "interactive": map[string]any{
		"type": "nfm_reply", "nfm_reply": map[string]any{"name": "flow", "body": "Sent", "response_json": string(b)},
	}})
	var m map[string]any
	_ = json.Unmarshal(resp, &m)
	m["from"], m["id"], m["timestamp"] = from, wamid, fmt.Sprint(time.Now().Unix())
	m["context"] = map[string]any{"id": replyTo}
	msg, _ := json.Marshal(m)
	err := h.webhook("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":"555001"},
		"contacts":[{"wa_id":%q,"profile":{"name":"Ravi"}}],"messages":[%s]}`, from, msg))
	if err != nil {
		h.t.Fatalf("flow reply: %v", err)
	}
}

func TestFlowsDesignerAndSubmissions(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	tenant := me.Tenant.ID

	var f flows.Flow
	c.do("POST", "/v1/flows", map[string]any{"whatsapp_account_id": phone.WhatsappAccountID, "name": ""}, http.StatusBadRequest, nil)
	c.do("POST", "/v1/flows", map[string]any{"whatsapp_account_id": phone.WhatsappAccountID, "name": "Lead", "categories": []string{"NOPE"}}, http.StatusBadRequest, nil)
	c.do("POST", "/v1/flows", map[string]any{"whatsapp_account_id": phone.WhatsappAccountID, "name": "Lead", "flow_json": map[string]any{"version": "6.3"}}, http.StatusBadRequest, nil)
	c.do("POST", "/v1/flows", map[string]any{"whatsapp_account_id": phone.WhatsappAccountID, "name": "Lead", "categories": []string{"LEAD_GENERATION"}}, http.StatusCreated, &f)
	if f.Status != "draft" || f.MetaFlowID != "flow-1" || f.PreviewURL == nil || len(h.meta.flowAssets) != 1 {
		t.Fatalf("flow = %+v, uploads = %d", f, len(h.meta.flowAssets))
	}

	// Meta's validation errors are kept and block publishing until fixed.
	h.meta.flowErrs = `[{"error":"INVALID_PROPERTY","error_type":"JSON_SCHEMA_ERROR","message":"Bad label"}]`
	var edited flows.Flow
	c.do("PUT", "/v1/flows/"+f.ID.String(), map[string]any{"name": "Lead form", "categories": []string{"LEAD_GENERATION"}}, http.StatusOK, &edited)
	if edited.Name != "Lead form" || string(edited.ValidationErrors) == "[]" {
		t.Fatalf("edited = %+v", edited)
	}
	c.do("POST", "/v1/flows/"+f.ID.String()+"/publish", nil, http.StatusUnprocessableEntity, nil)
	h.meta.flowErrs = ""
	c.do("PUT", "/v1/flows/"+f.ID.String(), map[string]any{"name": "Lead form", "categories": []string{"LEAD_GENERATION"}}, http.StatusOK, &edited)
	c.do("POST", "/v1/flows/"+f.ID.String()+"/publish", nil, http.StatusOK, &f)
	if f.Status != "published" || f.PublishedAt == nil {
		t.Fatalf("published = %+v", f)
	}
	c.do("PUT", "/v1/flows/"+f.ID.String(), map[string]any{"name": "x"}, http.StatusConflict, nil)
	c.do("DELETE", "/v1/flows/"+f.ID.String(), nil, http.StatusConflict, nil)

	// A draft can be deleted.
	var draft flows.Flow
	c.do("POST", "/v1/flows", map[string]any{"whatsapp_account_id": phone.WhatsappAccountID, "name": "Scratch"}, http.StatusCreated, &draft)
	c.do("DELETE", "/v1/flows/"+draft.ID.String(), nil, http.StatusNoContent, nil)
	c.do("GET", "/v1/flows/"+draft.ID.String(), nil, http.StatusNotFound, nil)

	// Send the Flow, then the customer submits it.
	var ep webhooks.CreatedEndpoint
	c.do("POST", "/v1/webhook-endpoints", map[string]any{"url": "https://hooks.example.com/f", "event_types": []string{"flow.submission"}}, http.StatusCreated, &ep)
	h.inbound(customer, "wamid.F1", "I want a quote")
	var msg messaging.Message
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "interactive", "interactive": map[string]any{
		"type": "flow", "body": map[string]any{"text": "Fill in your details"},
		"action": map[string]any{"name": "flow", "parameters": map[string]any{"flow_message_version": "3", "flow_token": "t1", "flow_id": f.MetaFlowID, "flow_cta": "Open"}},
	}}, "", http.StatusAccepted, &msg)
	if err := h.runSend(tenant, msg.ID, 1); err != nil {
		t.Fatal(err)
	}
	h.flowReply(customer, "wamid.F2", "wamid.TEST1", map[string]any{"flow_token": "t1", "name": "Ravi K", "phone": "9876500000", "interests": []string{"rice", "oil"}})
	h.flowReply(customer, "wamid.F2", "wamid.TEST1", map[string]any{"flow_token": "t1", "name": "Ravi K"}) // re-delivery

	var page struct {
		Data []flows.Submission
	}
	c.do("GET", "/v1/flow-submissions?flow_id="+f.ID.String(), nil, http.StatusOK, &page)
	if len(page.Data) != 1 {
		t.Fatalf("submissions = %+v", page.Data)
	}
	s := page.Data[0]
	var got map[string]any
	_ = json.Unmarshal(s.Response, &got)
	if s.FlowName == nil || *s.FlowName != "Lead form" || s.ContactWaID != customer || got["name"] != "Ravi K" || got["flow_token"] != nil {
		t.Fatalf("submission = %+v %v", s, got)
	}
	c.do("GET", "/v1/flow-submissions?contact_id="+s.ContactID.String(), nil, http.StatusOK, &page)
	if len(page.Data) != 1 {
		t.Fatalf("by contact = %d", len(page.Data))
	}
	c.do("GET", "/v1/flow-submissions?contact_id="+uuid.NewString(), nil, http.StatusOK, &page)
	if len(page.Data) != 0 {
		t.Fatalf("other contact = %d", len(page.Data))
	}
	var deliveries int
	h.scalar(tenant, "SELECT count(*) FROM river_job WHERE kind = 'deliver_webhook'", &deliveries)
	if deliveries != 1 {
		t.Fatalf("flow.submission deliveries = %d, want 1", deliveries)
	}

	// Meta reports the Flow was deprecated.
	err := h.webhook("flows", `{"event":"FLOW_STATUS_CHANGE","flow_id":"flow-1","old_status":"PUBLISHED","new_status":"DEPRECATED"}`)
	if err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/v1/flows/"+f.ID.String(), nil, http.StatusOK, &f)
	if f.Status != "deprecated" {
		t.Fatalf("status = %s", f.Status)
	}
}

func TestChatbotSendsFlowAndUsesAnswers(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	tenant := me.Tenant.ID
	var f flows.Flow
	c.do("POST", "/v1/flows", map[string]any{"whatsapp_account_id": phone.WhatsappAccountID, "name": "Lead"}, http.StatusCreated, &f)
	c.do("POST", "/v1/flows/"+f.ID.String()+"/publish", nil, http.StatusOK, &f)

	flow := map[string]any{
		"start":    "form",
		"triggers": []map[string]any{{"type": "keyword", "keywords": []string{"quote"}}},
		"nodes": map[string]any{
			"form":   map[string]any{"type": "flow", "text": "Tell us about you", "flow_id": f.ID, "cta": "Start", "var": "lead", "next": "thanks"},
			"thanks": map[string]any{"type": "message", "text": "Thanks {{lead_name}}, we will call {{lead_phone}}."},
		},
	}
	c.do("POST", "/v1/bots", map[string]any{"name": "Quote", "flow": map[string]any{"start": "form", "nodes": map[string]any{"form": map[string]any{"type": "flow", "text": "x", "flow_id": "nope"}}}}, http.StatusBadRequest, nil)
	var bot bots.Bot
	c.do("POST", "/v1/bots", map[string]any{"name": "Quote", "flow": flow}, http.StatusCreated, &bot)
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/activate", nil, http.StatusOK, &bot)

	h.inbound(customer, "wamid.Q1", "quote")
	h.runBotJobs(tenant)
	var msg struct{ Type, Mode, FlowID, Token, CTA string }
	h.scalar(tenant, `SELECT content->'interactive'->>'type', coalesce(content->'interactive'->'action'->'parameters'->>'mode', ''),
		content->'interactive'->'action'->'parameters'->>'flow_id', content->'interactive'->'action'->'parameters'->>'flow_token',
		content->'interactive'->'action'->'parameters'->>'flow_cta' FROM messages WHERE origin = 'bot'`,
		&msg.Type, &msg.Mode, &msg.FlowID, &msg.Token, &msg.CTA)
	var sess uuid.UUID
	h.scalar(tenant, "SELECT id FROM bot_sessions WHERE status = 'active'", &sess)
	if msg.Type != "flow" || msg.Mode != "" || msg.FlowID != f.MetaFlowID || msg.Token != sess.String() || msg.CTA != "Start" {
		t.Fatalf("flow message = %+v, session %s", msg, sess)
	}

	// Plain text while the form is open asks again; the submission carries on the conversation.
	h.flowReply(customer, "wamid.Q2", "wamid.UNKNOWN", map[string]any{"flow_token": sess.String(), "name": "Ravi", "phone": "98765"})
	h.runBotJobs(tenant)
	texts := h.botTexts(tenant)
	if len(texts) != 2 || texts[1] != "Thanks Ravi, we will call 98765." {
		t.Fatalf("bot sent %q", texts)
	}
	var status string
	h.scalar(tenant, "SELECT status FROM bot_sessions", &status)
	if status != "completed" {
		t.Fatalf("session = %s", status)
	}
}
