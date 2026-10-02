package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/metaevents"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
)

const customer = "919800000001"

// connected signs up a workspace and connects WABA 1100111 with number 555001.
func (h *harness) connected() (*client, auth.MeResponse, numbers.PhoneNumber) {
	h.t.Helper()
	c := h.newClient()
	me := c.signup("owner@example.com", "Sharma Sweets")
	var sess onboarding.SessionView
	c.do("POST", "/internal/onboarding/sessions", map[string]string{"flow": "standard"}, http.StatusCreated, &sess)
	c.do("POST", "/internal/onboarding/sessions/"+sess.ID.String()+"/complete", map[string]string{
		"code": "c", "waba_id": "1100111", "phone_number_id": "555001",
	}, http.StatusOK, &sess)
	if err := h.runJob(me.Tenant.ID, sess.ID, 1); err != nil {
		h.t.Fatalf("onboarding: %v", err)
	}
	var list struct{ Data []numbers.PhoneNumber }
	c.do("GET", "/v1/phone-numbers", nil, http.StatusOK, &list)
	return c, me, list.Data[0]
}

// webhook runs one Meta delivery through the event processor and returns its error.
func (h *harness) webhook(field, value string) error {
	body := fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"1100111","changes":[{"field":%q,"value":%s}]}]}`, field, value)
	sum := sha256.Sum256([]byte(body))
	return h.events.Work(context.Background(), &river.Job[metaevents.ProcessArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 10},
		Args:   metaevents.ProcessArgs{Payload: []byte(body), PayloadHash: hex.EncodeToString(sum[:]), ReceivedAt: time.Now()},
	})
}

func (h *harness) inbound(from, wamid, text string) {
	h.t.Helper()
	err := h.webhook("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":"555001"},
		"contacts":[{"wa_id":%q,"profile":{"name":"Ravi"}}],
		"messages":[{"from":%q,"id":%q,"timestamp":"%d","type":"text","text":{"body":%q}}]}`,
		from, from, wamid, time.Now().Unix(), text))
	if err != nil {
		h.t.Fatalf("inbound webhook: %v", err)
	}
}

func (h *harness) status(wamid, status string) error {
	return h.webhook("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":"555001"},
		"statuses":[{"id":%q,"status":%q,"timestamp":"%d","recipient_id":%q}]}`, wamid, status, time.Now().Unix(), customer))
}

// runSend works the send job for a message on attempt n.
func (h *harness) runSend(tenantID, messageID uuid.UUID, attempt int) error {
	return h.sender.Work(context.Background(), &river.Job[messaging.SendArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: 8},
		Args:   messaging.SendArgs{MessageID: messageID, TenantID: tenantID},
	})
}

// send posts to /v1/messages with an optional Idempotency-Key.
func (c *client) send(body any, key string, want int, out any) http.Header {
	c.h.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.h.api.URL+"/v1/messages", bytes.NewReader(b))
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
		c.h.t.Fatalf("POST /v1/messages = %d, want %d: %s", resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.h.t.Fatalf("decode: %v: %s", err, raw)
		}
	}
	return resp.Header
}

type apiErr struct {
	Error struct {
		Code          string `json:"code"`
		Param         string `json:"param"`
		MetaErrorCode int    `json:"meta_error_code"`
	} `json:"error"`
}

func text(phone uuid.UUID, body string) map[string]any {
	return map[string]any{"phone_number_id": phone, "to": customer, "type": "text", "text": map[string]any{"body": body}}
}

func TestSendTextRoundTrip(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()

	// The customer has never written, so free-form text is refused.
	var e apiErr
	c.send(text(phone.ID, "Hello"), "", http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "window_closed" {
		t.Fatalf("error = %+v, want window_closed", e.Error)
	}

	// The customer writes; now a reply is allowed and queued with its send job.
	h.inbound(customer, "wamid.IN1", "Is my order ready?")
	var msg messaging.Message
	c.send(text(phone.ID, "नमस्ते! It ships tomorrow."), "", http.StatusAccepted, &msg)
	if msg.Status != "queued" || msg.Wamid != nil || msg.Origin != "dashboard" || msg.Contact.WaID != customer {
		t.Fatalf("queued message = %+v", msg)
	}
	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		var n int
		err := tx.QueryRow(context.Background(), "SELECT count(*) FROM river_job WHERE kind = 'send_message' AND queue = 'messages'").Scan(&n)
		if n != 1 {
			t.Errorf("send jobs = %d, want 1", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A status that races ahead of the stored wamid is retried, not dropped.
	if err := h.status("wamid.TEST1", "sent"); err == nil {
		t.Fatal("status for a not-yet-stored wamid was accepted; want a retry")
	}

	if err := h.runSend(me.Tenant.ID, msg.ID, 1); err != nil {
		t.Fatalf("send worker: %v", err)
	}
	sent := h.meta.sent[len(h.meta.sent)-1]
	if sent["to"] != customer || sent["type"] != "text" || sent["messaging_product"] != "whatsapp" {
		t.Fatalf("Meta got %v", sent)
	}
	c.do("GET", "/v1/messages/"+msg.ID.String(), nil, http.StatusOK, &msg)
	if msg.Status != "sent" || msg.Wamid == nil || *msg.Wamid != "wamid.TEST1" {
		t.Fatalf("after send: %+v", msg)
	}

	// Running the job again (a River retry) does not send twice.
	if err := h.runSend(me.Tenant.ID, msg.ID, 2); err != nil || len(h.meta.sent) != 1 {
		t.Fatalf("second run: err %v, sends %d", err, len(h.meta.sent))
	}

	// Delivery ticks arrive as webhooks and only move forward.
	if err := h.status("wamid.TEST1", "read"); err != nil {
		t.Fatal(err)
	}
	if err := h.status("wamid.TEST1", "delivered"); err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/v1/messages/"+msg.ID.String(), nil, http.StatusOK, &msg)
	if msg.Status != "read" {
		t.Fatalf("status = %s, want read", msg.Status)
	}

	// Marking the customer's message read calls Meta.
	var inID uuid.UUID
	err = h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), "SELECT id FROM messages WHERE wamid = 'wamid.IN1'").Scan(&inID)
	})
	if err != nil {
		t.Fatal(err)
	}
	c.do("POST", "/v1/messages/"+inID.String()+"/read", nil, http.StatusNoContent, nil)
	c.do("POST", "/v1/messages/"+msg.ID.String()+"/read", nil, http.StatusUnprocessableEntity, nil)

	// Another workspace cannot see the message.
	other := h.newClient()
	other.signup("other@example.com", "Other Shop")
	other.do("GET", "/v1/messages/"+msg.ID.String(), nil, http.StatusNotFound, nil)
	other.send(text(phone.ID, "hi"), "", http.StatusNotFound, nil)
}

func TestSendIdempotency(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	h.inbound(customer, "wamid.IN2", "hi")

	var first, again messaging.Message
	c.send(text(phone.ID, "Your order shipped"), "order-42", http.StatusAccepted, &first)
	hdr := c.send(text(phone.ID, "Your order shipped"), "order-42", http.StatusAccepted, &again)
	if again.ID != first.ID || hdr.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %v (header %q), want %v", again.ID, hdr.Get("Idempotent-Replayed"), first.ID)
	}
	var e apiErr
	c.send(text(phone.ID, "Different text"), "order-42", http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "idempotency_conflict" {
		t.Fatalf("error = %+v", e.Error)
	}
	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		var n int
		err := tx.QueryRow(context.Background(), "SELECT count(*) FROM messages WHERE direction = 'outbound'").Scan(&n)
		if n != 1 {
			t.Errorf("outbound messages = %d, want 1", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendChecksContactAndRequest(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	h.inbound(customer, "wamid.IN3", "hi")

	var e apiErr
	c.send(map[string]any{"phone_number_id": phone.ID, "to": "+91 98", "type": "text", "text": map[string]any{"body": "x"}},
		"", http.StatusBadRequest, &e)
	if e.Error.Param != "to" {
		t.Fatalf("bad number error = %+v", e.Error)
	}
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "image", "text": map[string]any{"body": "x"}},
		"", http.StatusBadRequest, nil)
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "image",
		"image": map[string]any{"link": "http://insecure.example/a.jpg"}}, "", http.StatusBadRequest, nil)
	c.send(map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "image",
		"image": map[string]any{"link": "https://cdn.example/a.jpg", "caption": "New stock"}}, "", http.StatusAccepted, nil)

	err := h.db.InTenant(context.Background(), me.Tenant.ID, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE contacts SET opt_in_status = 'opted_out' WHERE wa_id = $1", customer)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	c.send(text(phone.ID, "hello again"), "", http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "contact_opted_out" {
		t.Fatalf("opted-out error = %+v", e.Error)
	}
}

func TestSendFailureMapping(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	h.inbound(customer, "wamid.IN4", "hi")
	var msg messaging.Message
	c.send(text(phone.ID, "hello"), "", http.StatusAccepted, &msg)

	// Throttling is retried and leaves the message queued.
	h.meta.sendErr = `{"error":{"message":"Rate limit hit","code":130429}}`
	err := h.runSend(me.Tenant.ID, msg.ID, 1)
	var cancel *river.JobCancelError
	if err == nil || errorsAs(err, &cancel) {
		t.Fatalf("throttled send err = %v, want a retryable error", err)
	}
	c.do("GET", "/v1/messages/"+msg.ID.String(), nil, http.StatusOK, &msg)
	if msg.Status != "queued" {
		t.Fatalf("after throttling: %s", msg.Status)
	}

	// An undeliverable number fails the message with a stable code.
	h.meta.sendErr = `{"error":{"message":"Message Undeliverable","code":131026}}`
	if err := h.runSend(me.Tenant.ID, msg.ID, 2); err == nil || !errorsAs(err, &cancel) {
		t.Fatalf("undeliverable send err = %v, want JobCancel", err)
	}
	c.do("GET", "/v1/messages/"+msg.ID.String(), nil, http.StatusOK, &msg)
	if msg.Status != "failed" || msg.Error == nil || msg.Error.Code != "recipient_not_on_whatsapp" ||
		msg.Error.MetaErrorCode == nil || *msg.Error.MetaErrorCode != 131026 {
		t.Fatalf("failed message = %+v (error %+v)", msg, msg.Error)
	}
}

func TestTemplatesSyncCreateSendDelete(t *testing.T) {
	h := newHarness(t)
	h.meta.templates = []string{
		`{"id":"8001","name":"order_shipped","language":"hi","status":"APPROVED","category":"UTILITY",
		  "components":[{"type":"BODY","text":"आपका ऑर्डर {{1}} भेज दिया गया है।","example":{"body_text":[["ORD-1"]]}}],
		  "quality_score":{"score":"GREEN"}}`,
		`{"id":"8002","name":"diwali_offer","language":"en","status":"REJECTED","category":"MARKETING",
		  "rejected_reason":"PROMOTIONAL","components":[{"type":"BODY","text":"Sale!"}]}`,
	}
	c, _, phone := h.connected() // onboarding syncs existing templates

	var list struct {
		Data       []templates.Template `json:"data"`
		NextCursor *string              `json:"next_cursor"`
	}
	c.do("GET", "/v1/templates", nil, http.StatusOK, &list)
	if len(list.Data) != 2 {
		t.Fatalf("templates after onboarding = %+v", list.Data)
	}
	c.do("GET", "/v1/templates?status=approved", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.Data[0].Name != "order_shipped" || list.Data[0].QualityScore == nil {
		t.Fatalf("approved templates = %+v", list.Data)
	}
	c.do("GET", "/v1/templates?limit=1", nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.NextCursor == nil {
		t.Fatalf("first page = %+v", list)
	}
	c.do("GET", "/v1/templates?limit=1&cursor="+*list.NextCursor, nil, http.StatusOK, &list)
	if len(list.Data) != 1 || list.NextCursor != nil {
		t.Fatalf("second page = %+v", list)
	}

	// Templates are allowed outside the 24-hour window, but only approved ones with the right variables.
	tpl := func(name, lang string, params ...string) map[string]any {
		var ps []map[string]string
		for _, p := range params {
			ps = append(ps, map[string]string{"type": "text", "text": p})
		}
		t := map[string]any{"name": name, "language": lang}
		if ps != nil {
			t["components"] = []map[string]any{{"type": "body", "parameters": ps}}
		}
		return map[string]any{"phone_number_id": phone.ID, "to": customer, "type": "template", "template": t}
	}
	var e apiErr
	c.send(tpl("order_shipped", "hi"), "", http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "template_param_mismatch" {
		t.Fatalf("missing params error = %+v", e.Error)
	}
	c.send(tpl("diwali_offer", "en"), "", http.StatusUnprocessableEntity, &e)
	if e.Error.Code != "template_not_approved" {
		t.Fatalf("rejected template error = %+v", e.Error)
	}
	var msg messaging.Message
	c.send(tpl("order_shipped", "hi", "ORD-4821"), "", http.StatusAccepted, &msg)
	if msg.Type != "template" {
		t.Fatalf("template message = %+v", msg)
	}

	// Creating submits to Meta and stores the template as pending.
	var created templates.Template
	c.do("POST", "/v1/templates", map[string]any{
		"whatsapp_account_id": phone.WhatsappAccountID, "name": "payment_due", "language": "en", "category": "utility",
		"components": []map[string]any{{"type": "BODY", "text": "Payment of {{1}} is due.", "example": map[string]any{"body_text": [][]string{{"₹500"}}}}},
	}, http.StatusCreated, &created)
	if created.Status != "pending" || created.MetaTemplateID == nil || h.meta.created[0]["category"] != "UTILITY" {
		t.Fatalf("created = %+v, meta got %v", created, h.meta.created)
	}
	c.do("POST", "/v1/templates", map[string]any{
		"whatsapp_account_id": phone.WhatsappAccountID, "name": "Bad Name", "language": "en", "category": "utility",
		"components": []map[string]any{{"type": "BODY", "text": "x"}},
	}, http.StatusBadRequest, nil)

	// A Meta review webhook approves it.
	if err := h.webhook("message_template_status_update", fmt.Sprintf(
		`{"event":"APPROVED","message_template_id":%s,"message_template_name":"payment_due","message_template_language":"en","reason":"NONE"}`,
		*created.MetaTemplateID)); err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/v1/templates/"+created.ID.String(), nil, http.StatusOK, &created)
	if created.Status != "approved" {
		t.Fatalf("after webhook: %s", created.Status)
	}

	// Editing resubmits for review.
	c.do("PATCH", "/v1/templates/"+created.ID.String(), map[string]any{
		"components": []map[string]any{{"type": "BODY", "text": "Your payment of {{1}} is due today."}},
	}, http.StatusOK, &created)
	if created.Status != "pending" {
		t.Fatalf("after edit: %s", created.Status)
	}

	// Delete removes every language of the name.
	c.do("DELETE", "/v1/templates/by-name/payment_due?whatsapp_account_id="+phone.WhatsappAccountID.String(), nil, http.StatusNoContent, nil)
	c.do("GET", "/v1/templates/"+created.ID.String(), nil, http.StatusNotFound, nil)

	// A sync drops templates deleted at Meta and picks up new ones.
	h.meta.templates = h.meta.templates[:1]
	var synced struct{ Synced int }
	c.do("POST", "/internal/templates/sync", nil, http.StatusOK, &synced)
	c.do("GET", "/v1/templates", nil, http.StatusOK, &list)
	if synced.Synced != 1 || len(list.Data) != 1 {
		t.Fatalf("after sync: %d synced, %+v", synced.Synced, list.Data)
	}
}
