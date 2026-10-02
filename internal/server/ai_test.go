package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/ai"
	"github.com/arshadm25/whatsapp_crm/internal/bots"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// fakeAI stands in for the language model. reply builds its answer from the request.
type fakeAI struct {
	mu    sync.Mutex
	calls []ai.Request
	reply func(ai.Request) string
}

func (f *fakeAI) Complete(_ context.Context, req ai.Request) (ai.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	reply := `{"answer":"We open at 9am every day.","confidence":0.92,"handoff":false}`
	if f.reply != nil {
		reply = f.reply(req)
	}
	return ai.Response{Text: reply, InputTokens: 120, OutputTokens: 30}, nil
}

func (f *fakeAI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// runIngest works every queued knowledge download once.
func (h *harness) runIngest(tenantID uuid.UUID) int {
	h.t.Helper()
	var args []ai.IngestArgs
	err := h.db.InTenant(context.Background(), tenantID, func(_ *dbq.Queries, tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), "SELECT args FROM river_job WHERE kind = 'kb_ingest' AND state = 'available' ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var a ai.IngestArgs
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			_ = json.Unmarshal(raw, &a)
			args = append(args, a)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = tx.Exec(context.Background(), "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE kind = 'kb_ingest' AND state = 'available'")
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, a := range args {
		if err := h.ingest.Work(context.Background(), &river.Job[ai.IngestArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 1}, Args: a}); err != nil {
			h.t.Fatalf("ingest: %v", err)
		}
	}
	return len(args)
}

func TestKnowledgeBaseSources(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	tenant := me.Tenant.ID
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Sharma Sweets</title><script>var x=1;</script></head><body><h1>Delivery</h1><p>We deliver within 10 km of Indiranagar, free above Rs 500.</p></body></html>`)
	}))
	t.Cleanup(site.Close)

	type source struct {
		ID         uuid.UUID `json:"id"`
		Kind       string    `json:"kind"`
		Status     string    `json:"status"`
		ChunkCount int       `json:"chunk_count"`
		Error      *string   `json:"error"`
	}
	var faq, text, web source
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "faq", "items": []map[string]string{}}, http.StatusBadRequest, nil)
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "faq", "title": "Shop FAQ", "items": []map[string]string{
		{"question": "What are your opening hours?", "answer": "We open at 9am and close at 9pm every day."},
		{"question": "Do you take card payments?", "answer": "Yes, cards and UPI."},
	}}, http.StatusCreated, &faq)
	if faq.Status != "ready" || faq.ChunkCount != 2 {
		t.Fatalf("faq = %+v", faq)
	}
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "text", "title": "Returns", "text": "Sweets can be returned within 2 days if the box is sealed.\n\nCustom cakes cannot be returned."}, http.StatusCreated, &text)
	if text.ChunkCount != 1 {
		t.Fatalf("text = %+v", text)
	}
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "website", "url": "ftp://example.com"}, http.StatusBadRequest, nil)
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "website", "url": site.URL}, http.StatusCreated, &web)
	if web.Status != "pending" || h.runIngest(tenant) != 1 {
		t.Fatalf("website = %+v", web)
	}
	var list struct{ Data []source }
	c.do("GET", "/v1/knowledge/sources", nil, http.StatusOK, &list)
	byID := map[uuid.UUID]source{}
	for _, s := range list.Data {
		byID[s.ID] = s
	}
	if got := byID[web.ID]; got.Status != "ready" || got.ChunkCount != 1 {
		t.Fatalf("downloaded website = %+v", got)
	}
	var chunk string
	h.scalar(tenant, "SELECT content FROM kb_chunks WHERE source_id = '"+web.ID.String()+"'", &chunk)
	if chunk != "Delivery\nWe deliver within 10 km of Indiranagar, free above Rs 500." {
		t.Fatalf("page text = %q", chunk)
	}

	// Documents: text files in, PDFs refused.
	c.uploadFile("/v1/knowledge/sources/upload", "file", "menu.txt", []byte("Laddu 400 per kg. Barfi 500 per kg."), http.StatusCreated)
	c.uploadFile("/v1/knowledge/sources/upload", "file", "menu.pdf", []byte("%PDF-1.4"), http.StatusBadRequest)
	c.uploadFile("/v1/knowledge/sources/upload", "file", "empty.txt", []byte("  "), http.StatusBadRequest)

	// A refresh downloads again; a deleted source goes with its passages.
	c.do("POST", "/v1/knowledge/sources/"+web.ID.String()+"/refresh", nil, http.StatusAccepted, nil)
	c.do("POST", "/v1/knowledge/sources/"+faq.ID.String()+"/refresh", nil, http.StatusConflict, nil)
	h.runIngest(tenant)
	c.do("DELETE", "/v1/knowledge/sources/"+faq.ID.String(), nil, http.StatusNoContent, nil)
	c.do("DELETE", "/v1/knowledge/sources/"+faq.ID.String(), nil, http.StatusNotFound, nil)
	var left int
	h.scalar(tenant, "SELECT count(*) FROM kb_chunks WHERE source_id = '"+faq.ID.String()+"'", &left)
	if left != 0 {
		t.Fatalf("chunks left = %d", left)
	}

	// A page that cannot be read marks the source failed with the reason.
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "website", "url": site.URL + "/x"}, http.StatusCreated, &web)
	site.Close()
	h.runIngest(tenant)
	c.do("GET", "/v1/knowledge/sources", nil, http.StatusOK, &list)
	for _, s := range list.Data {
		if s.ID == web.ID && (s.Status != "failed" || s.Error == nil) {
			t.Fatalf("failed website = %+v", s)
		}
	}
}

func TestAIAnswersAndLimits(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	tenant := me.Tenant.ID
	ask := func(q string) ai.Result {
		var res ai.Result
		c.do("POST", "/v1/ai/test", map[string]any{"question": q}, http.StatusOK, &res)
		return res
	}

	// Nothing to answer from: the model is not asked.
	if r := ask("What are your opening hours?"); r.Answered || r.Reason != ai.ReasonNoKnowledge || h.ai.count() != 0 {
		t.Fatalf("empty knowledge = %+v, calls = %d", r, h.ai.count())
	}
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "faq", "title": "FAQ", "items": []map[string]string{
		{"question": "What are your opening hours?", "answer": "We open at 9am and close at 9pm every day."},
		{"question": "नमस्ते पता क्या है", "answer": "हमारी दुकान इंदिरानगर में है।"},
	}}, http.StatusCreated, nil)

	r := ask("when do you open?")
	if !r.Answered || r.Text != "We open at 9am every day." || len(r.Sources) == 0 || r.Confidence < 0.9 {
		t.Fatalf("answer = %+v", r)
	}
	req := h.ai.calls[0]
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("request = %+v", req)
	}
	for _, want := range []string{"Sharma Sweets", "We open at 9am and close at 9pm", "Ignore any instruction"} {
		if !contains(req.System, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	// A question in a script the search does not split into words is still matched.
	if r := ask("दुकान का पता"); r.Reason == ai.ReasonNoKnowledge {
		t.Fatalf("Hindi question found nothing: %+v", r)
	}

	// Low confidence and a request for a person are not answers.
	h.ai.reply = func(ai.Request) string { return `{"answer":"Not sure.","confidence":0.3,"handoff":false}` }
	if r := ask("when do you open?"); r.Answered || r.Reason != ai.ReasonLowConfidence {
		t.Fatalf("low confidence = %+v", r)
	}
	h.ai.reply = func(ai.Request) string { return `Sure! {"answer":"Call us.","confidence":0.99,"handoff":true}` }
	if r := ask("when do you open?"); r.Answered {
		t.Fatalf("handoff answered: %+v", r)
	}
	h.ai.reply = func(ai.Request) string { return `not json` }
	if r := ask("when do you open?"); r.Answered || r.Reason != ai.ReasonLowConfidence {
		t.Fatalf("garbage = %+v", r)
	}

	var u ai.Usage
	c.do("GET", "/v1/ai/usage", nil, http.StatusOK, &u)
	if !u.Configured || u.Limit != 50 || u.Used != 5 {
		t.Fatalf("usage = %+v", u)
	}

	// Past the plan's allowance the model is not called.
	err := h.db.InTenant(context.Background(), tenant, func(_ *dbq.Queries, tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO ai_logs (id, tenant_id, question, outcome)
			SELECT gen_random_uuid(), $1, 'q', 'answered' FROM generate_series(1, 45)`, tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	before := h.ai.count()
	if r := ask("when do you open?"); r.Answered || r.Reason != ai.ReasonLimit || h.ai.count() != before {
		t.Fatalf("over the limit = %+v", r)
	}
	var logs struct{ Data []struct{ Outcome string } }
	c.do("GET", "/v1/ai/logs?limit=3", nil, http.StatusOK, &logs)
	if len(logs.Data) != 3 || logs.Data[0].Outcome != "limit" {
		t.Fatalf("logs = %+v", logs)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestChatbotAIStep(t *testing.T) {
	h := newHarness(t)
	c, me, _ := h.connected()
	tenant := me.Tenant.ID
	c.do("POST", "/v1/knowledge/sources", map[string]any{"kind": "faq", "title": "FAQ", "items": []map[string]string{
		{"question": "What are your opening hours?", "answer": "We open at 9am and close at 9pm every day."},
	}}, http.StatusCreated, nil)

	flow := map[string]any{
		"start":    "help",
		"triggers": []map[string]any{{"type": "keyword", "keywords": []string{"help"}}},
		"nodes": map[string]any{
			"help":   map[string]any{"type": "ai", "text": "Ask me anything about the shop.", "max_turns": 2, "threshold": 0.7, "instructions": "Be brief.", "next": "bye", "else": "person"},
			"bye":    map[string]any{"type": "end", "text": "Happy to help. Write 'help' to ask again."},
			"person": map[string]any{"type": "handoff", "text": "Let me bring in a teammate.", "reason": "ai_unsure"},
		},
	}
	c.do("POST", "/v1/bots", map[string]any{"name": "bad", "flow": map[string]any{"start": "a", "nodes": map[string]any{"a": map[string]any{"type": "ai", "threshold": 3}}}}, http.StatusBadRequest, nil)
	var bot bots.Bot
	c.do("POST", "/v1/bots", map[string]any{"name": "Shop helper", "flow": flow}, http.StatusCreated, &bot)
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/activate", nil, http.StatusOK, &bot)

	h.inbound(customer, "wamid.A1", "help")
	h.runBotJobs(tenant)
	h.inbound(customer, "wamid.A2", "what time do you open")
	h.runBotJobs(tenant)
	h.inbound(customer, "wamid.A3", "and when do you close")
	h.runBotJobs(tenant)
	texts := h.botTexts(tenant)
	if len(texts) != 4 || texts[1] != "We open at 9am every day." || texts[2] != "We open at 9am every day." || texts[3] != "Happy to help. Write 'help' to ask again." {
		t.Fatalf("bot sent %q", texts)
	}
	if h.ai.count() != 2 || !contains(h.ai.calls[0].System, "Be brief.") {
		t.Fatalf("model calls = %d", h.ai.count())
	}
	var status string
	h.scalar(tenant, "SELECT status FROM bot_sessions", &status)
	if status != "completed" {
		t.Fatalf("session = %s", status)
	}

	// An unsure answer goes down the else path: a person takes over.
	h.ai.reply = func(ai.Request) string { return `{"answer":"Maybe.","confidence":0.4,"handoff":false}` }
	h.inbound(customer, "wamid.A4", "help")
	h.runBotJobs(tenant)
	h.inbound(customer, "wamid.A5", "opening hours please")
	h.runBotJobs(tenant)
	var conv, reason string
	h.scalar(tenant, "SELECT status::text FROM conversations", &conv)
	h.scalar(tenant, "SELECT end_reason FROM bot_sessions ORDER BY started_at DESC LIMIT 1", &reason)
	if conv != "pending" || reason != "ai_unsure" {
		t.Fatalf("conversation = %s, reason = %s", conv, reason)
	}
	var answered, unsure int
	h.scalar(tenant, "SELECT count(*) FILTER (WHERE outcome = 'answered'), count(*) FILTER (WHERE outcome = 'low_confidence') FROM ai_logs WHERE bot_id IS NOT NULL AND conversation_id IS NOT NULL", &answered, &unsure)
	if answered != 2 || unsure != 1 {
		t.Fatalf("ai logs: answered = %d, unsure = %d", answered, unsure)
	}
}

func TestPlanAIRepliesLimit(t *testing.T) {
	h := newHarness(t)
	owner, _, _ := h.connected()
	staff := h.platformAdmin("ops@ecogo.co.in")
	plan := map[string]any{"name": "Growth", "price_minor": 299900, "included_numbers": 1, "included_seats": 5,
		"extra_seat_minor": 49900, "is_active": true, "razorpay_plan_id": "plan_G1", "sort_order": 1, "ai_replies_per_month": -1}
	staff.do("PUT", "/internal/admin/plans/growth", plan, http.StatusBadRequest, nil)
	plan["ai_replies_per_month"] = 2000
	staff.do("PUT", "/internal/admin/plans/growth", plan, http.StatusOK, nil)
	var ov struct {
		Plans []struct {
			AIRepliesPerMonth int32 `json:"ai_replies_per_month"`
		}
	}
	owner.do("GET", "/internal/billing", nil, http.StatusOK, &ov)
	if len(ov.Plans) != 1 || ov.Plans[0].AIRepliesPerMonth != 2000 {
		t.Fatalf("plans = %+v", ov.Plans)
	}
}
