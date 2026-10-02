package bots

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/ai"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/flows"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

// StepArgs is the River job that advances a conversation's bot: after an inbound message
// (MessageID) or when someone starts a bot by hand (BotID).
type StepArgs struct {
	TenantID       uuid.UUID  `json:"tenant_id"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	MessageID      *uuid.UUID `json:"message_id,omitempty"`
	BotID          *uuid.UUID `json:"bot_id,omitempty"`
	StartID        uuid.UUID  `json:"start_id,omitempty"` // makes each manual start a new job
}

func (StepArgs) Kind() string { return "bot_step" }

func (StepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       jobs.QueueBots,
		MaxAttempts: 5,
		UniqueOpts:  river.UniqueOpts{ByArgs: true},
	}
}

// sendGap spaces a bot's consecutive messages, so they reach the customer in order.
const sendGap = 1200 * time.Millisecond

// sessionTTL is how long a session waits for an answer. WhatsApp closes the free-text window
// after 24 hours, so a bot cannot carry on past it.
const sessionTTL = messaging.Window

// HandoffEvent is the data of a bot.handoff webhook event.
type HandoffEvent struct {
	BotID          uuid.UUID         `json:"bot_id"`
	BotName        string            `json:"bot_name"`
	SessionID      uuid.UUID         `json:"session_id"`
	ConversationID uuid.UUID         `json:"conversation_id"`
	ContactID      uuid.UUID         `json:"contact_id"`
	ContactWaID    string            `json:"contact_wa_id"`
	Reason         string            `json:"reason"`
	Variables      map[string]string `json:"variables"`
}

// Worker runs bot steps.
//
// The conversation row is locked for the whole step, so two messages arriving together are
// handled one after the other. Guardrails: a bot never writes to a blocked or opted-out
// contact, in a conversation a person has taken over, past the 24-hour window (except with
// an approved template), or for a workspace whose plan has ended.
type Worker struct {
	river.WorkerDefaults[StepArgs]
	db  *db.DB
	log *slog.Logger
	now func() time.Time
	// Jobs enqueues sends and webhook deliveries; when nil, the River client working the job is used.
	Jobs jobs.Inserter
	// AI answers the questions of AI steps; when nil, an AI step hands over to a person.
	AI *ai.Agent
}

func NewWorker(d *db.DB, log *slog.Logger) *Worker {
	return &Worker{db: d, log: log, now: time.Now}
}

func (w *Worker) Timeout(*river.Job[StepArgs]) time.Duration { return time.Minute }

func (w *Worker) Work(ctx context.Context, job *river.Job[StepArgs]) error {
	a := job.Args
	return w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		return w.step(ctx, q, tx, a)
	})
}

// inbound is the part of a stored inbound message the bot reads.
type inbound struct {
	Type string `json:"type"`
	Text *struct {
		Body string `json:"body"`
	} `json:"text"`
	Button *struct {
		Text    string `json:"text"`
		Payload string `json:"payload"`
	} `json:"button"`
	Interactive *struct {
		ButtonReply *struct{ ID, Title string } `json:"button_reply"`
		ListReply   *struct{ ID, Title string } `json:"list_reply"`
		NfmReply    *struct {
			Name         string `json:"name"`
			ResponseJSON string `json:"response_json"`
		} `json:"nfm_reply"`
	} `json:"interactive"`
}

func parseInput(raw []byte) Input {
	var m inbound
	if json.Unmarshal(raw, &m) != nil {
		return Input{}
	}
	switch {
	case m.Interactive != nil && m.Interactive.NfmReply != nil:
		if _, answers, err := flows.ParseResponse(m.Interactive.NfmReply.ResponseJSON); err == nil {
			return Input{Flow: flows.Flatten(answers)}
		}
		return Input{}
	case m.Interactive != nil && m.Interactive.ButtonReply != nil:
		return Input{Text: m.Interactive.ButtonReply.Title, ButtonID: m.Interactive.ButtonReply.ID}
	case m.Interactive != nil && m.Interactive.ListReply != nil:
		return Input{Text: m.Interactive.ListReply.Title, ButtonID: m.Interactive.ListReply.ID}
	case m.Button != nil:
		return Input{Text: m.Button.Text, ButtonID: m.Button.Payload}
	case m.Text != nil:
		return Input{Text: m.Text.Body}
	}
	return Input{}
}

func (w *Worker) step(ctx context.Context, q *dbq.Queries, tx pgx.Tx, a StepArgs) error {
	row, err := q.GetBotConversation(ctx, a.ConversationID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	conv, contact := row.Conversation, row.Contact
	now := w.now()

	sess, err := q.GetActiveBotSession(ctx, conv.ID)
	if err != nil && !db.IsNotFound(err) {
		return err
	}
	active := err == nil

	// A person owns the conversation: the bot steps aside.
	if conv.AssigneeUserID != nil || conv.Status == dbq.ConversationStatusPending {
		if active {
			return w.end(ctx, q, sess, StatusStopped, "agent_took_over", sess.NodeID, nil)
		}
		return nil
	}
	if active {
		human, err := q.HasHumanReplySince(ctx, dbq.HasHumanReplySinceParams{ConversationID: conv.ID, Since: sess.StartedAt})
		if err != nil {
			return err
		}
		if human {
			return w.end(ctx, q, sess, StatusStopped, "agent_replied", sess.NodeID, nil)
		}
		if now.Sub(sess.UpdatedAt) > sessionTTL {
			if err := w.end(ctx, q, sess, StatusExpired, "expired", sess.NodeID, nil); err != nil {
				return err
			}
			active = false
		}
	}
	if contact.Blocked || contact.OptInStatus == dbq.OptInStatusOptedOut {
		if active {
			return w.end(ctx, q, sess, StatusStopped, "contact_opted_out", sess.NodeID, nil)
		}
		return nil
	}
	if err := billing.Check(ctx, q, now); err != nil {
		w.log.Info("bot step skipped: plan has ended", "tenant_id", a.TenantID)
		return nil
	}
	num, err := q.GetSendingNumber(ctx, conv.PhoneNumberID)
	if err != nil {
		return err
	}
	if num.PhoneNumber.Status != dbq.ConnectionStatusConnected || num.WhatsappAccount.Status != dbq.ConnectionStatusConnected {
		return nil
	}

	var in *Input
	if a.MessageID != nil {
		m, err := q.GetInboundMessage(ctx, *a.MessageID)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		i := parseInput(m.Content)
		in = &i
	}
	c := Contact{Phone: contact.WaID}
	if n := displayName(contact); n != "" {
		c.Name = n
	}

	var (
		bot   dbq.Bot
		out   Outcome
		aiErr error
	)
	switch {
	case active && in != nil:
		bot, err = q.GetBot(ctx, sess.BotID)
		if err != nil {
			return err
		}
		f, err := ParseFlow(bot.Flow)
		if err != nil {
			return w.end(ctx, q, sess, StatusFailed, "invalid_flow", sess.NodeID, nil)
		}
		var vars map[string]string
		_ = json.Unmarshal(sess.Vars, &vars)
		env := w.env(ctx, q, a.TenantID, bot, conv, c, &aiErr)
		out = Resume(f, State{NodeID: deref(sess.NodeID), Vars: vars}, *in, env)
	case active:
		return nil // a manual start while a session runs, or a replayed job
	default:
		var f Flow
		bot, f, err = w.pick(ctx, q, a, conv, in)
		if err != nil || bot.ID == uuid.Nil {
			return err
		}
		sess, err = q.InsertBotSession(ctx, dbq.InsertBotSessionParams{
			ID: db.NewID(), TenantID: a.TenantID, BotID: bot.ID, ConversationID: conv.ID,
			ContactID: contact.ID, Vars: []byte("{}"),
		})
		if err != nil {
			return err
		}
		out = Start(f, w.env(ctx, q, a.TenantID, bot, conv, c, &aiErr))
	}
	if aiErr != nil {
		return aiErr
	}
	return w.apply(ctx, q, tx, num, conv, contact, bot, sess, out, now)
}

// pick chooses the bot a new session runs: the one named by a manual start, or the first
// active bot for this number whose trigger the message matches.
func (w *Worker) pick(ctx context.Context, q *dbq.Queries, a StepArgs, conv dbq.Conversation, in *Input) (dbq.Bot, Flow, error) {
	if a.BotID != nil {
		b, err := q.GetBot(ctx, *a.BotID)
		if db.IsNotFound(err) || (err == nil && b.Status != "active") {
			return dbq.Bot{}, Flow{}, nil
		}
		if err != nil {
			return dbq.Bot{}, Flow{}, err
		}
		f, err := ParseFlow(b.Flow)
		return b, f, err
	}
	if in == nil {
		return dbq.Bot{}, Flow{}, nil
	}
	n, err := q.CountInboundMessages(ctx, conv.ID)
	if err != nil {
		return dbq.Bot{}, Flow{}, err
	}
	list, err := q.ListActiveBotsForNumber(ctx, &conv.PhoneNumberID)
	if err != nil {
		return dbq.Bot{}, Flow{}, err
	}
	for _, b := range list {
		f, err := ParseFlow(b.Flow)
		if err != nil {
			w.log.Warn("active bot has an invalid flow", "bot_id", b.ID)
			continue
		}
		if f.Matches(*in, n == 1) {
			return b, f, nil
		}
	}
	return dbq.Bot{}, Flow{}, nil
}

// apply carries out an outcome: stores and queues the messages, tags the contact, hands over
// and saves the session.
func (w *Worker) apply(ctx context.Context, q *dbq.Queries, tx pgx.Tx, num dbq.GetSendingNumberRow, conv dbq.Conversation,
	contact dbq.Contact, bot dbq.Bot, sess dbq.BotSession, out Outcome, now time.Time) error {
	ins, err := jobs.From(ctx, w.Jobs)
	if err != nil {
		return err
	}
	status, reason := out.Status, out.Reason
	sent := 0
	var preview string
actions:
	for _, act := range out.Actions {
		switch act.Kind {
		case ActionSend, ActionTemplate, ActionFlow:
			msgType, content, templateID, ok, why, err := w.prepare(ctx, q, num, conv, act, sess.ID, now)
			if err != nil {
				return err
			}
			if !ok {
				status, reason = StatusFailed, why
				out.State.NodeID = ""
				break actions
			}
			body, err := json.Marshal(content)
			if err != nil {
				return err
			}
			msg, err := q.InsertBotMessage(ctx, dbq.InsertBotMessageParams{
				ID: db.NewID(), TenantID: conv.TenantID, ConversationID: conv.ID, PhoneNumberID: conv.PhoneNumberID,
				ContactID: contact.ID, Type: msgType, Content: body, TemplateID: templateID, BotID: &bot.ID,
			})
			if err != nil {
				return err
			}
			var opts *river.InsertOpts
			if sent > 0 {
				opts = &river.InsertOpts{ScheduledAt: now.Add(time.Duration(sent) * sendGap)}
			}
			if _, err := ins.InsertTx(ctx, tx, messaging.SendArgs{MessageID: msg.ID, TenantID: conv.TenantID}, opts); err != nil {
				return err
			}
			sent++
			preview = act.Preview
		case ActionTag:
			id, err := q.UpsertTag(ctx, dbq.UpsertTagParams{TenantID: conv.TenantID, Name: act.Tag})
			if err != nil {
				return err
			}
			if err := q.AddContactTag(ctx, dbq.AddContactTagParams{TenantID: conv.TenantID, ContactID: contact.ID, TagID: id}); err != nil {
				return err
			}
		case ActionHandoff:
			if err := w.handoff(ctx, q, tx, ins, bot, sess, conv, contact, act, out.State.Vars); err != nil {
				return err
			}
		}
	}
	if sent > 0 {
		if err := q.TouchConversationOutbound(ctx, dbq.TouchConversationOutboundParams{ID: conv.ID, At: now, Preview: preview}); err != nil {
			return err
		}
	}
	return w.end(ctx, q, sess, status, reason, nodeOrNil(out.State.NodeID), out.State.Vars)
}

// prepare applies the send guardrails and builds the stored message.
func (w *Worker) prepare(ctx context.Context, q *dbq.Queries, num dbq.GetSendingNumberRow, conv dbq.Conversation, act Action, sessionID uuid.UUID, now time.Time) (msgType dbq.MessageType, content map[string]any, templateID *uuid.UUID, ok bool, why string, err error) {
	if act.Kind == ActionSend || act.Kind == ActionFlow {
		if conv.LastInboundAt == nil || now.Sub(*conv.LastInboundAt) >= messaging.Window {
			return "", nil, nil, false, "window_closed", nil
		}
		if act.Kind == ActionFlow {
			return w.flowMessage(ctx, q, act.FlowNode, sessionID)
		}
		return dbq.MessageType(act.Type), act.Content, nil, true, "", nil
	}
	t, err := q.GetTemplateByName(ctx, dbq.GetTemplateByNameParams{
		WhatsappAccountID: num.WhatsappAccount.ID, Name: act.Template.Name, Language: act.Template.Language,
	})
	if db.IsNotFound(err) || (err == nil && t.Status != dbq.TemplateStatusApproved) {
		return "", nil, nil, false, "template_not_approved", nil
	}
	if err != nil {
		return "", nil, nil, false, "", err
	}
	tpl := map[string]any{"name": t.Name, "language": map[string]string{"code": t.Language}}
	if len(act.Template.Params) > 0 {
		params := make([]map[string]any, len(act.Template.Params))
		for i, p := range act.Template.Params {
			params[i] = map[string]any{"type": "text", "text": p}
		}
		tpl["components"] = []map[string]any{{"type": "body", "parameters": params}}
	}
	return dbq.MessageTypeTemplate, map[string]any{"type": "template", "template": tpl}, &t.ID, true, "", nil
}

func (w *Worker) handoff(ctx context.Context, q *dbq.Queries, tx pgx.Tx, ins jobs.Inserter, bot dbq.Bot, sess dbq.BotSession,
	conv dbq.Conversation, contact dbq.Contact, act Action, vars map[string]string) error {
	var assignee *uuid.UUID
	if id, err := uuid.Parse(act.AssignTo); err == nil {
		if _, err := q.GetMemberRole(ctx, id); err == nil {
			assignee = &id
		}
	}
	st := dbq.ConversationStatusPending
	if assignee != nil {
		st = dbq.ConversationStatusOpen
	}
	err := q.UpdateConversation(ctx, dbq.UpdateConversationParams{
		ID: conv.ID, Status: &st,
		SetAssignee: assignee != nil, AssigneeUserID: assignee,
	})
	if err != nil {
		return err
	}
	return webhooks.Emit(ctx, q, tx, ins, conv.TenantID, webhooks.BotHandoff, &conv.PhoneNumberID, HandoffEvent{
		BotID: bot.ID, BotName: bot.Name, SessionID: sess.ID, ConversationID: conv.ID, ContactID: contact.ID,
		ContactWaID: contact.WaID, Reason: act.Reason, Variables: publicVars(vars),
	})
}

// end saves the session's new state. Ending statuses stamp ended_at.
func (w *Worker) end(ctx context.Context, q *dbq.Queries, sess dbq.BotSession, status, reason string, node *string, vars map[string]string) error {
	if vars == nil {
		_ = json.Unmarshal(sess.Vars, &vars)
	}
	raw, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	var r *string
	if status != StatusActive {
		r = &reason
	}
	if status != StatusActive {
		node = nil
	}
	return q.SaveBotSession(ctx, dbq.SaveBotSessionParams{ID: sess.ID, NodeID: node, Vars: raw, Status: status, EndReason: r})
}

func publicVars(v map[string]string) map[string]string {
	out := make(map[string]string, len(v))
	for k, val := range v {
		if !strings.HasPrefix(k, "_") {
			out[k] = val
		}
	}
	return out
}

func nodeOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func displayName(c dbq.Contact) string {
	if c.Name != nil {
		return *c.Name
	}
	if c.ProfileName != nil {
		return *c.ProfileName
	}
	return ""
}

// flowMessage builds the interactive message that opens one of the workspace's Flows. The
// session id travels as the flow token, so the submission finds its way back to the bot.
func (w *Worker) flowMessage(ctx context.Context, q *dbq.Queries, n *Node, sessionID uuid.UUID) (dbq.MessageType, map[string]any, *uuid.UUID, bool, string, error) {
	id, err := uuid.Parse(n.FlowID)
	if err != nil {
		return "", nil, nil, false, "flow_unavailable", nil
	}
	f, err := q.GetFlow(ctx, id)
	if db.IsNotFound(err) || (err == nil && f.Status != "published" && f.Status != "draft") {
		return "", nil, nil, false, "flow_unavailable", nil
	}
	if err != nil {
		return "", nil, nil, false, "", err
	}
	cta := n.CTA
	if cta == "" {
		cta = "Open"
	}
	params := map[string]any{
		"flow_message_version": "3", "flow_token": sessionID.String(), "flow_id": f.MetaFlowID, "flow_cta": cta,
	}
	if f.Status == "draft" {
		params["mode"] = "draft"
	}
	if n.Screen != "" {
		params["flow_action"] = "navigate"
		params["flow_action_payload"] = map[string]any{"screen": n.Screen}
	}
	return dbq.MessageTypeInteractive, map[string]any{"type": "interactive", "interactive": map[string]any{
		"type": "flow", "body": map[string]any{"text": n.Text}, "action": map[string]any{"name": "flow", "parameters": params},
	}}, nil, true, "", nil
}

// env gives a run the customer's details and, for AI steps, the knowledge base agent. A failure
// in the agent (the database, not the model) is kept in aiErr so the step is retried.
func (w *Worker) env(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, bot dbq.Bot, conv dbq.Conversation, c Contact, aiErr *error) Env {
	env := Env{Contact: c}
	if w.AI == nil {
		return env
	}
	env.AI = func(question string, n Node) AIResult {
		t, err := q.GetTenant(ctx, tenantID)
		if err != nil {
			*aiErr = err
			return AIResult{Reason: ai.ReasonError}
		}
		res, err := w.AI.Answer(ctx, q, tenantID, t.Name, question, ai.Options{
			Instructions: n.Instructions, Threshold: n.Threshold, BotID: &bot.ID, ConversationID: &conv.ID,
		})
		if err != nil {
			*aiErr = err
			return AIResult{Reason: ai.ReasonError}
		}
		return AIResult{Answered: res.Answered, Text: res.Text, Reason: res.Reason}
	}
	return env
}
