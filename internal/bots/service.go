package bots

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"log/slog"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
)

type Service struct {
	db       *db.DB
	inserter jobs.Inserter
	log      *slog.Logger
}

func NewService(d *db.DB, inserter jobs.Inserter, log *slog.Logger) *Service {
	return &Service{db: d, inserter: inserter, log: log}
}

var manage = auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin)

// Routes mounts /v1/bots.
func (s *Service) Routes(r chi.Router) {
	r.Use(manage)
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Post("/", httpx.Handler(s.log, s.create))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Put("/{id}", httpx.Handler(s.log, s.update))
	r.Delete("/{id}", httpx.Handler(s.log, s.remove))
	r.Post("/{id}/activate", httpx.Handler(s.log, s.activate))
	r.Post("/{id}/pause", httpx.Handler(s.log, s.pause))
	r.Post("/{id}/assign", httpx.Handler(s.log, s.assign))
	r.Post("/{id}/start", httpx.Handler(s.log, s.start))
	r.Post("/{id}/stop", httpx.Handler(s.log, s.stop))
	r.Get("/{id}/sessions", httpx.Handler(s.log, s.sessions))
}

// EnqueueInbound queues the bot step for an inbound message, when the workspace runs a bot
// or the conversation has a session. Call it in the transaction that stores the message.
func EnqueueInbound(ctx context.Context, q *dbq.Queries, tx pgx.Tx, ins jobs.Inserter, tenantID, conversationID, messageID uuid.UUID) error {
	used, err := q.BotsInUse(ctx, conversationID)
	if err != nil || used == nil || !*used {
		return err
	}
	ins, err = jobs.From(ctx, ins)
	if err != nil {
		return err
	}
	_, err = ins.InsertTx(ctx, tx, StepArgs{TenantID: tenantID, ConversationID: conversationID, MessageID: &messageID}, nil)
	return err
}

// Bot matches the Bot schema in api/openapi.yaml.
type Bot struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	Status        string          `json:"status"`
	PhoneNumberID *uuid.UUID      `json:"phone_number_id"`
	Flow          json.RawMessage `json:"flow"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func view(b dbq.Bot) Bot {
	return Bot{ID: b.ID, Name: b.Name, Status: b.Status, PhoneNumberID: b.PhoneNumberID, Flow: b.Flow,
		CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}

// Session matches the BotSession schema in api/openapi.yaml.
type Session struct {
	ID             uuid.UUID         `json:"id"`
	BotID          uuid.UUID         `json:"bot_id"`
	ConversationID uuid.UUID         `json:"conversation_id"`
	ContactID      uuid.UUID         `json:"contact_id"`
	Status         string            `json:"status"`
	EndReason      *string           `json:"end_reason"`
	Variables      map[string]string `json:"variables"`
	StartedAt      time.Time         `json:"started_at"`
	EndedAt        *time.Time        `json:"ended_at"`
}

func sessionView(x dbq.BotSession) Session {
	var vars map[string]string
	_ = json.Unmarshal(x.Vars, &vars)
	return Session{ID: x.ID, BotID: x.BotID, ConversationID: x.ConversationID, ContactID: x.ContactID, Status: x.Status,
		EndReason: x.EndReason, Variables: publicVars(vars), StartedAt: x.StartedAt, EndedAt: x.EndedAt}
}

type botRequest struct {
	Name          string          `json:"name"`
	PhoneNumberID *uuid.UUID      `json:"phone_number_id"`
	Flow          json.RawMessage `json:"flow"`
}

func (s *Service) check(ctx context.Context, q *dbq.Queries, p auth.Principal, req *botRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(req.Name); n < 1 || n > 100 {
		return httpx.BadRequest("name", "name must have 1 to 100 characters.")
	}
	if _, err := ParseFlow(req.Flow); err != nil {
		return err
	}
	return s.checkNumber(ctx, q, p, req.PhoneNumberID)
}

func (s *Service) checkNumber(ctx context.Context, q *dbq.Queries, p auth.Principal, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	_, err := q.GetSendingNumber(ctx, *id)
	if db.IsNotFound(err) || (err == nil && !p.AllowsNumber(*id)) {
		return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Param: "phone_number_id", Message: "Phone number not found."}
	}
	return err
}

func botID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

// withBot runs fn on the bot named in the URL, inside the tenant.
func (s *Service) withBot(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, q *dbq.Queries, tx pgx.Tx, p auth.Principal, b dbq.Bot) (any, int, error)) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := botID(r)
	if err != nil {
		return err
	}
	var (
		out    any
		status int
	)
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		b, err := q.GetBot(r.Context(), id)
		if db.IsNotFound(err) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if b.PhoneNumberID != nil && !p.AllowsNumber(*b.PhoneNumberID) {
			return httpx.ErrNotFound
		}
		out, status, err = fn(r.Context(), q, tx, p, b)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, status, out)
	return nil
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []Bot{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListBots(r.Context())
		for _, b := range rows {
			if b.PhoneNumberID == nil || p.AllowsNumber(*b.PhoneNumberID) {
				out = append(out, view(b))
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req botRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	var out Bot
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if err := s.check(r.Context(), q, p, &req); err != nil {
			return err
		}
		b, err := q.InsertBot(r.Context(), dbq.InsertBotParams{
			ID: db.NewID(), TenantID: p.TenantID, Name: req.Name, PhoneNumberID: req.PhoneNumberID, Flow: req.Flow, CreatedBy: p.User(),
		})
		out = view(b)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	return s.withBot(w, r, func(_ context.Context, _ *dbq.Queries, _ pgx.Tx, _ auth.Principal, b dbq.Bot) (any, int, error) {
		return view(b), http.StatusOK, nil
	})
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) error {
	var req botRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	return s.withBot(w, r, func(ctx context.Context, q *dbq.Queries, _ pgx.Tx, p auth.Principal, b dbq.Bot) (any, int, error) {
		if err := s.check(ctx, q, p, &req); err != nil {
			return nil, 0, err
		}
		u, err := q.UpdateBot(ctx, dbq.UpdateBotParams{ID: b.ID, Name: req.Name, Flow: req.Flow, PhoneNumberID: req.PhoneNumberID})
		return view(u), http.StatusOK, err
	})
}

func (s *Service) remove(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := botID(r)
	if err != nil {
		return err
	}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		n, err := q.DeleteBot(r.Context(), id)
		if err == nil && n == 0 {
			return httpx.ErrNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) activate(w http.ResponseWriter, r *http.Request) error {
	return s.withBot(w, r, func(ctx context.Context, q *dbq.Queries, _ pgx.Tx, _ auth.Principal, b dbq.Bot) (any, int, error) {
		if _, err := ParseFlow(b.Flow); err != nil {
			return nil, 0, err
		}
		u, err := q.SetBotStatus(ctx, dbq.SetBotStatusParams{ID: b.ID, Status: "active"})
		return view(u), http.StatusOK, err
	})
}

// pause stops the bot taking new conversations and ends the sessions it has open.
func (s *Service) pause(w http.ResponseWriter, r *http.Request) error {
	return s.withBot(w, r, func(ctx context.Context, q *dbq.Queries, _ pgx.Tx, _ auth.Principal, b dbq.Bot) (any, int, error) {
		u, err := q.SetBotStatus(ctx, dbq.SetBotStatusParams{ID: b.ID, Status: "paused"})
		if err != nil {
			return nil, 0, err
		}
		return view(u), http.StatusOK, q.StopBotSessionsForBot(ctx, b.ID)
	})
}

// assign sets the number the bot answers on; null means every number.
func (s *Service) assign(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		PhoneNumberID *uuid.UUID `json:"phone_number_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	return s.withBot(w, r, func(ctx context.Context, q *dbq.Queries, _ pgx.Tx, p auth.Principal, b dbq.Bot) (any, int, error) {
		if err := s.checkNumber(ctx, q, p, req.PhoneNumberID); err != nil {
			return nil, 0, err
		}
		u, err := q.UpdateBot(ctx, dbq.UpdateBotParams{ID: b.ID, Name: b.Name, Flow: b.Flow, PhoneNumberID: req.PhoneNumberID})
		return view(u), http.StatusOK, err
	})
}

type conversationRequest struct {
	ConversationID uuid.UUID `json:"conversation_id"`
}

// start runs the bot on a conversation now, for example a customer who wrote before it was switched on.
func (s *Service) start(w http.ResponseWriter, r *http.Request) error {
	var req conversationRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	return s.withBot(w, r, func(ctx context.Context, q *dbq.Queries, tx pgx.Tx, p auth.Principal, b dbq.Bot) (any, int, error) {
		if b.Status != "active" {
			return nil, 0, unprocessable("bot_not_active", "Activate the bot before starting it on a conversation.")
		}
		row, err := q.GetConversationView(ctx, req.ConversationID)
		if db.IsNotFound(err) || (err == nil && (!p.AllowsNumber(row.Conversation.PhoneNumberID) ||
			(b.PhoneNumberID != nil && *b.PhoneNumberID != row.Conversation.PhoneNumberID))) {
			return nil, 0, &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Param: "conversation_id", Message: "Conversation not found for this bot's number."}
		}
		if err != nil {
			return nil, 0, err
		}
		if _, err := q.GetActiveBotSession(ctx, row.Conversation.ID); err == nil {
			return nil, 0, httpx.NewError(http.StatusConflict, "conflict", "A bot is already running in this conversation. Stop it first.")
		} else if !db.IsNotFound(err) {
			return nil, 0, err
		}
		ins, err := jobs.From(ctx, s.inserter)
		if err != nil {
			return nil, 0, err
		}
		_, err = ins.InsertTx(ctx, tx, StepArgs{TenantID: p.TenantID, ConversationID: row.Conversation.ID, BotID: &b.ID, StartID: db.NewID()}, nil)
		return map[string]string{"status": "queued"}, http.StatusAccepted, err
	})
}

// stop ends the bot's session in a conversation; the conversation stays as it is.
func (s *Service) stop(w http.ResponseWriter, r *http.Request) error {
	var req conversationRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	return s.withBot(w, r, func(ctx context.Context, q *dbq.Queries, _ pgx.Tx, _ auth.Principal, b dbq.Bot) (any, int, error) {
		sess, err := q.GetActiveBotSession(ctx, req.ConversationID)
		if db.IsNotFound(err) || (err == nil && sess.BotID != b.ID) {
			return nil, 0, httpx.ErrNotFound
		}
		if err != nil {
			return nil, 0, err
		}
		reason := "stopped"
		err = q.SaveBotSession(ctx, dbq.SaveBotSessionParams{ID: sess.ID, Vars: sess.Vars, Status: StatusStopped, EndReason: &reason})
		sess.Status, sess.EndReason = StatusStopped, &reason
		return sessionView(sess), http.StatusOK, err
	})
}

func (s *Service) sessions(w http.ResponseWriter, r *http.Request) error {
	return s.withBot(w, r, func(ctx context.Context, q *dbq.Queries, _ pgx.Tx, _ auth.Principal, b dbq.Bot) (any, int, error) {
		rows, err := q.ListBotSessions(ctx, dbq.ListBotSessionsParams{BotID: b.ID, Lim: 100})
		out := make([]Session, len(rows))
		for i, x := range rows {
			out[i] = sessionView(x)
		}
		return map[string]any{"data": out}, http.StatusOK, err
	})
}

func unprocessable(code, message string) error {
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: code, Message: message}
}
