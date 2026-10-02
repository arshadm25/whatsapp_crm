package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/bots"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

// runBotJobs works every queued bot step once, the way River would, and returns how many ran.
func (h *harness) runBotJobs(tenantID uuid.UUID) int {
	h.t.Helper()
	var args []bots.StepArgs
	err := h.db.InTenant(context.Background(), tenantID, func(_ *dbq.Queries, tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), "SELECT args FROM river_job WHERE kind = 'bot_step' AND state = 'available' ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var a bots.StepArgs
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			_ = json.Unmarshal(raw, &a)
			args = append(args, a)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = tx.Exec(context.Background(), "UPDATE river_job SET state = 'completed', finalized_at = now() WHERE kind = 'bot_step' AND state = 'available'")
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, a := range args {
		err := h.bots.Work(context.Background(), &river.Job[bots.StepArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 5}, Args: a})
		if err != nil {
			h.t.Fatalf("bot step: %v", err)
		}
	}
	return len(args)
}

// scalar runs a query that returns one row inside the tenant.
func (h *harness) scalar(tenantID uuid.UUID, sql string, dest ...any) {
	h.t.Helper()
	err := h.db.InTenant(context.Background(), tenantID, func(_ *dbq.Queries, tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), sql).Scan(dest...)
	})
	if err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

// botTexts lists what the bot has sent to the conversation, oldest first.
func (h *harness) botTexts(tenantID uuid.UUID) []string {
	h.t.Helper()
	var out []string
	err := h.db.InTenant(context.Background(), tenantID, func(_ *dbq.Queries, tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT coalesce(content->'text'->>'body', content->'interactive'->'body'->>'text', 'template:'||(content->'template'->>'name'))
			FROM messages WHERE origin = 'bot' AND bot_id IS NOT NULL ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *harness) buttonReply(from, wamid, id, title string) {
	h.t.Helper()
	err := h.webhook("messages", fmt.Sprintf(`{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"919876543210","phone_number_id":"555001"},
		"contacts":[{"wa_id":%q,"profile":{"name":"Ravi"}}],
		"messages":[{"from":%q,"id":%q,"timestamp":"%d","type":"interactive",
		"interactive":{"type":"button_reply","button_reply":{"id":%q,"title":%q}}}]}`,
		from, from, wamid, time.Now().Unix(), id, title))
	if err != nil {
		h.t.Fatalf("button reply: %v", err)
	}
}

func menuFlow() map[string]any {
	return map[string]any{
		"start":    "menu",
		"triggers": []map[string]any{{"type": "keyword", "keywords": []string{"menu", "hi"}}},
		"nodes": map[string]any{
			"menu": map[string]any{"type": "buttons", "text": "Hi {{contact.name}}, how can we help?", "buttons": []map[string]any{
				{"id": "sales", "title": "Sales", "next": "ask_email"},
				{"id": "agent", "title": "Talk to a person", "next": "human"},
			}},
			"ask_email": map[string]any{"type": "question", "text": "What is your email?", "var": "email", "kind": "email", "next": "tag"},
			"tag":       map[string]any{"type": "tag", "tag": "lead", "next": "thanks"},
			"thanks":    map[string]any{"type": "message", "text": "Thanks {{email}}, we will write soon.", "next": "done"},
			"done":      map[string]any{"type": "end"},
			"human":     map[string]any{"type": "handoff", "text": "Connecting you to a person.", "reason": "wants_agent"},
		},
	}
}

func TestChatbotConversation(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	tenant := me.Tenant.ID

	// A bot must be valid before it is saved.
	bad := menuFlow()
	bad["nodes"].(map[string]any)["tag"].(map[string]any)["next"] = "missing"
	c.do("POST", "/v1/bots", map[string]any{"name": "Menu", "flow": bad}, http.StatusBadRequest, nil)
	var ep webhooks.CreatedEndpoint
	c.do("POST", "/v1/webhook-endpoints", map[string]any{"url": "https://hooks.example.com/bots", "event_types": []string{"bot.handoff"}}, http.StatusCreated, &ep)
	var bot bots.Bot
	c.do("POST", "/v1/bots", map[string]any{"name": "Menu", "phone_number_id": phone.ID, "flow": menuFlow()}, http.StatusCreated, &bot)
	if bot.Status != "draft" {
		t.Fatalf("new bot = %+v", bot)
	}

	// A draft bot does not answer.
	h.inbound(customer, "wamid.B1", "menu")
	if n := h.runBotJobs(tenant); n != 0 {
		t.Fatalf("draft bot queued %d steps", n)
	}
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/activate", nil, http.StatusOK, &bot)
	if bot.Status != "active" {
		t.Fatalf("bot = %+v", bot)
	}

	// The keyword starts the bot: it asks with buttons, spaced behind the customer's message.
	h.inbound(customer, "wamid.B2", "Menu")
	if n := h.runBotJobs(tenant); n != 1 {
		t.Fatalf("steps = %d", n)
	}
	if got := h.botTexts(tenant); len(got) != 1 || got[0] != "Hi Ravi, how can we help?" {
		t.Fatalf("bot sent %q", got)
	}
	var active int
	h.scalar(tenant, "SELECT count(*) FROM bot_sessions WHERE status = 'active' AND node_id = 'menu'", &active)
	if active != 1 {
		t.Fatalf("active sessions = %d", active)
	}

	// Tapping Sales moves on to the question. A bad answer is asked again, a good one is stored.
	h.buttonReply(customer, "wamid.B3", "sales", "Sales")
	h.runBotJobs(tenant)
	h.inbound(customer, "wamid.B4", "not an email")
	h.runBotJobs(tenant)
	if got := h.botTexts(tenant); len(got) != 3 || got[1] != "What is your email?" || got[2] != "What is your email?" {
		t.Fatalf("bot sent %q", got)
	}
	h.inbound(customer, "wamid.B5", "ravi@example.com")
	h.runBotJobs(tenant)
	got := h.botTexts(tenant)
	if len(got) != 4 || got[3] != "Thanks ravi@example.com, we will write soon." {
		t.Fatalf("bot sent %q", got)
	}
	var status, reason string
	h.scalar(tenant, "SELECT status, end_reason FROM bot_sessions", &status, &reason)
	if status != "completed" {
		t.Fatalf("session = %s %s", status, reason)
	}
	var tagged int
	h.scalar(tenant, "SELECT count(*) FROM contact_tags ct JOIN tags t ON t.id = ct.tag_id WHERE t.name = 'lead'", &tagged)
	if tagged != 1 {
		t.Fatalf("lead tags = %d", tagged)
	}

	// Every bot message went out through the normal send path, ordered by schedule.
	var sends, scheduled int
	h.scalar(tenant, "SELECT count(*), count(*) FILTER (WHERE state = 'scheduled') FROM river_job WHERE kind = 'send_message'", &sends, &scheduled)
	if sends != 4 || scheduled != 0 {
		t.Fatalf("send jobs = %d, scheduled = %d", sends, scheduled)
	}

	// Choosing a person hands the conversation over, with the greeting scheduled after the tap.
	h.inbound(customer, "wamid.B6", "hi")
	h.runBotJobs(tenant)
	h.buttonReply(customer, "wamid.B7", "agent", "Talk to a person")
	h.runBotJobs(tenant)
	var conv string
	h.scalar(tenant, "SELECT status::text FROM conversations", &conv)
	if conv != "pending" {
		t.Fatalf("conversation status = %s", conv)
	}
	h.scalar(tenant, "SELECT status, end_reason FROM bot_sessions ORDER BY started_at DESC LIMIT 1", &status, &reason)
	if status != "handed_off" || reason != "wants_agent" {
		t.Fatalf("session = %s %s", status, reason)
	}
	var handoffs int
	h.scalar(tenant, "SELECT count(*) FROM river_job WHERE kind = 'deliver_webhook'", &handoffs)
	if handoffs != 1 {
		t.Fatalf("bot.handoff deliveries = %d, want 1", handoffs)
	}
	// While a person owns it, the bot stays quiet even for its keyword.
	before := len(h.botTexts(tenant))
	h.inbound(customer, "wamid.B8", "menu")
	h.runBotJobs(tenant)
	if after := len(h.botTexts(tenant)); after != before {
		t.Fatalf("bot wrote into a handed-off conversation: %q", h.botTexts(tenant))
	}
}

func TestChatbotGuardrails(t *testing.T) {
	h := newHarness(t)
	c, me, phone := h.connected()
	tenant := me.Tenant.ID
	var bot bots.Bot
	c.do("POST", "/v1/bots", map[string]any{"name": "Menu", "flow": menuFlow()}, http.StatusCreated, &bot)
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/activate", nil, http.StatusOK, &bot)

	// An opted-out contact is never messaged.
	h.inbound(customer, "wamid.G1", "STOP")
	h.inbound(customer, "wamid.G2", "menu")
	h.runBotJobs(tenant)
	if got := h.botTexts(tenant); len(got) != 0 {
		t.Fatalf("bot wrote to an opted-out contact: %q", got)
	}
	h.inbound(customer, "wamid.G3", "START")
	h.runBotJobs(tenant)

	// A bot bound to another number does not answer on this one.
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/assign", map[string]any{"phone_number_id": uuid.New()}, http.StatusNotFound, nil)
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/assign", map[string]any{"phone_number_id": phone.ID}, http.StatusOK, &bot)

	// Pausing ends open sessions and stops new ones.
	h.inbound(customer, "wamid.G4", "menu")
	h.runBotJobs(tenant)
	if got := h.botTexts(tenant); len(got) != 1 {
		t.Fatalf("bot sent %q", got)
	}
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/pause", nil, http.StatusOK, &bot)
	var open int
	h.scalar(tenant, "SELECT count(*) FROM bot_sessions WHERE status = 'active'", &open)
	if bot.Status != "paused" || open != 0 {
		t.Fatalf("bot = %+v, open sessions = %d", bot, open)
	}
	h.inbound(customer, "wamid.G5", "menu")
	if n := h.runBotJobs(tenant); n != 0 {
		t.Fatalf("paused bot queued %d steps", n)
	}

	// Start by hand on a conversation, then stop it.
	var convID uuid.UUID
	h.scalar(tenant, "SELECT id FROM conversations", &convID)
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/start", map[string]any{"conversation_id": convID}, http.StatusUnprocessableEntity, nil)
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/activate", nil, http.StatusOK, &bot)
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/start", map[string]any{"conversation_id": convID}, http.StatusAccepted, nil)
	h.runBotJobs(tenant)
	if got := h.botTexts(tenant); len(got) != 2 {
		t.Fatalf("bot sent %q", got)
	}
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/start", map[string]any{"conversation_id": convID}, http.StatusConflict, nil)
	var sess bots.Session
	c.do("POST", "/v1/bots/"+bot.ID.String()+"/stop", map[string]any{"conversation_id": convID}, http.StatusOK, &sess)
	if sess.Status != "stopped" {
		t.Fatalf("session = %+v", sess)
	}
	var list struct{ Data []bots.Session }
	c.do("GET", "/v1/bots/"+bot.ID.String()+"/sessions", nil, http.StatusOK, &list)
	if len(list.Data) != 2 {
		t.Fatalf("sessions = %d", len(list.Data))
	}
	c.do("DELETE", "/v1/bots/"+bot.ID.String(), nil, http.StatusNoContent, nil)
	c.do("GET", "/v1/bots/"+bot.ID.String(), nil, http.StatusNotFound, nil)
}
