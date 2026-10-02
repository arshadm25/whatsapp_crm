// Package inbox serves D3: conversations, their messages, assignment, internal notes and quick
// replies. Sending is internal/messaging; live updates are internal/events.
package inbox

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
)

type Service struct {
	db  *db.DB
	log *slog.Logger
	now func() time.Time
}

func NewService(d *db.DB, log *slog.Logger) *Service { return &Service{db: d, log: log, now: time.Now} }

// Routes mounts /v1/conversations.
func (s *Service) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(s.log, s.list))
	r.Get("/{id}", httpx.Handler(s.log, s.get))
	r.Patch("/{id}", httpx.Handler(s.log, s.update))
	r.Get("/{id}/messages", httpx.Handler(s.log, s.messages))
}

// InternalRoutes mounts /internal/inbox for the dashboard: team members, notes and quick replies.
func (s *Service) InternalRoutes(r chi.Router) {
	r.Get("/members", httpx.Handler(s.log, s.members))
	r.Get("/conversations/{id}/notes", httpx.Handler(s.log, s.notes))
	r.Post("/conversations/{id}/notes", httpx.Handler(s.log, s.addNote))
	r.Get("/quick-replies", httpx.Handler(s.log, s.quickReplies))
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
		r.Post("/quick-replies", httpx.Handler(s.log, s.addQuickReply))
		r.Delete("/quick-replies/{id}", httpx.Handler(s.log, s.deleteQuickReply))
	})
}

// Conversation matches the Conversation schema in api/openapi.yaml.
type Conversation struct {
	ID                 uuid.UUID        `json:"id"`
	PhoneNumberID      uuid.UUID        `json:"phone_number_id"`
	Contact            contacts.Contact `json:"contact"`
	Status             string           `json:"status"`
	AssigneeID         *uuid.UUID       `json:"assignee_id"`
	Window             Window           `json:"window"`
	UnreadCount        int32            `json:"unread_count"`
	LastMessageAt      *time.Time       `json:"last_message_at"`
	LastMessagePreview *string          `json:"last_message_preview"`
}

// Window is the customer service window: free-form messages are allowed until it expires.
type Window struct {
	Open      bool       `json:"open"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Service) view(cv dbq.Conversation, ct dbq.Contact, tags []string) Conversation {
	v := Conversation{
		ID: cv.ID, PhoneNumberID: cv.PhoneNumberID, Contact: contacts.View(ct, tags), Status: string(cv.Status),
		AssigneeID: cv.AssigneeUserID, UnreadCount: cv.UnreadCount, LastMessageAt: cv.LastMessageAt,
		LastMessagePreview: cv.LastMessagePreview,
	}
	if cv.LastInboundAt != nil {
		exp := cv.LastInboundAt.Add(messaging.Window)
		v.Window.ExpiresAt = &exp
		v.Window.Open = s.now().Before(exp)
	}
	return v
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	params := dbq.ListConversationsParams{}
	lim, err := httpx.Limit(qs.Get("limit"))
	if err != nil {
		return err
	}
	if v := qs.Get("phone_number_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("phone_number_id", "phone_number_id must be a UUID.")
		}
		params.PhoneNumberID = &id
	}
	if v := qs.Get("status"); v != "" {
		st := dbq.ConversationStatus(v)
		if !st.Valid() {
			return httpx.BadRequest("status", "status must be open, pending or closed.")
		}
		params.Status = &st
	}
	switch v := qs.Get("assignee_id"); v {
	case "":
	case "none":
		params.Unassigned = true
	case "me":
		if p.IsAPIKey() {
			return httpx.BadRequest("assignee_id", "An API key is not a team member; filter by a user ID instead of me.")
		}
		params.AssigneeID = &p.UserID
	default:
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("assignee_id", "assignee_id must be a user ID, me or none.")
		}
		params.AssigneeID = &id
	}
	switch v := qs.Get("window"); v {
	case "":
	case "open", "closed":
		open := v == "open"
		params.WindowOpen = &open
	default:
		return httpx.BadRequest("window", "window must be open or closed.")
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		esc := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(v)
		params.Search = &esc
	}
	if v := qs.Get("cursor"); v != "" {
		at, id, ok := httpx.DecodeCursor(v)
		if !ok {
			return httpx.BadRequest("cursor", "Invalid cursor.")
		}
		params.BeforeAt, params.BeforeID = &at, &id
	}
	params.Lim = lim + 1

	out := []Conversation{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListConversations(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			last := rows[len(rows)-1]
			c := httpx.EncodeCursor(last.ActivityAt, last.Conversation.ID)
			next = &c
		}
		ids := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			ids[i] = row.Contact.ID
		}
		tagRows, err := q.ListContactTags(r.Context(), ids)
		if err != nil {
			return err
		}
		tags := contacts.Tags(tagRows)
		for _, row := range rows {
			out = append(out, s.view(row.Conversation, row.Contact, tags[row.Contact.ID]))
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

// load returns one conversation of the tenant in the API shape.
func (s *Service) load(r *http.Request, q *dbq.Queries, id uuid.UUID) (Conversation, error) {
	row, err := q.GetConversationView(r.Context(), id)
	if err != nil {
		return Conversation{}, err
	}
	tagRows, err := q.ListContactTags(r.Context(), []uuid.UUID{row.Contact.ID})
	if err != nil {
		return Conversation{}, err
	}
	return s.view(row.Conversation, row.Contact, contacts.Tags(tagRows)[row.Contact.ID]), nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var out Conversation
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		out, err = s.load(r, q, id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req struct {
		Status     *string         `json:"status"`
		AssigneeID json.RawMessage `json:"assignee_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	params := dbq.UpdateConversationParams{ID: id}
	if req.Status != nil {
		st := dbq.ConversationStatus(*req.Status)
		if !st.Valid() {
			return httpx.BadRequest("status", "status must be open, pending or closed.")
		}
		params.Status = &st
	}
	if len(req.AssigneeID) > 0 {
		params.SetAssignee = true
		if string(req.AssigneeID) != "null" {
			var a uuid.UUID
			if err := json.Unmarshal(req.AssigneeID, &a); err != nil {
				return httpx.BadRequest("assignee_id", "assignee_id must be a user ID or null.")
			}
			params.AssigneeUserID = &a
		}
	}
	if params.Status == nil && !params.SetAssignee {
		return httpx.BadRequest("", "Send status, assignee_id or both.")
	}
	var out Conversation
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if params.AssigneeUserID != nil {
			ok, err := q.IsMember(r.Context(), *params.AssigneeUserID)
			if err != nil {
				return err
			}
			if !ok {
				return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "invalid_request", Param: "assignee_id",
					Message: "That user is not a member of this workspace."}
			}
		}
		if _, err := q.GetConversationView(r.Context(), id); err != nil {
			return err
		}
		if err := q.UpdateConversation(r.Context(), params); err != nil {
			return err
		}
		out, err = s.load(r, q, id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) messages(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	lim, err := httpx.Limit(r.URL.Query().Get("limit"))
	if err != nil {
		return err
	}
	params := dbq.ListConversationMessagesParams{ConversationID: id, Lim: lim + 1}
	if v := r.URL.Query().Get("cursor"); v != "" {
		c, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("cursor", "Invalid cursor.")
		}
		params.Before = &c
	}
	out := []messaging.Message{}
	var next *string
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		conv, err := q.GetConversationView(r.Context(), id)
		if err != nil {
			return err
		}
		rows, err := q.ListConversationMessages(r.Context(), params)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			c := rows[len(rows)-1].ID.String()
			next = &c
		}
		name := conv.Contact.Name
		if name == nil {
			name = conv.Contact.ProfileName
		}
		for _, m := range rows {
			out = append(out, messaging.View(m, conv.Contact.WaID, name))
		}
		return nil
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
	return nil
}

type Member struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}

func (s *Service) members(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []Member{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListMembers(r.Context())
		for _, m := range rows {
			out = append(out, Member{ID: m.ID, Name: m.Name, Email: m.Email, Role: string(m.Role)})
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

type Note struct {
	ID         uuid.UUID `json:"id"`
	Body       string    `json:"body"`
	AuthorID   uuid.UUID `json:"author_id"`
	AuthorName string    `json:"author_name"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Service) notes(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	out := []Note{}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := q.GetConversationView(r.Context(), id); err != nil {
			return err
		}
		rows, err := q.ListNotes(r.Context(), id)
		for _, n := range rows {
			out = append(out, Note{ID: n.ID, Body: n.Body, AuthorID: n.AuthorUserID, AuthorName: n.AuthorName, CreatedAt: n.CreatedAt})
		}
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Service) addNote(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" || len(req.Body) > 4000 {
		return httpx.BadRequest("body", "A note needs 1 to 4000 characters.")
	}
	var n dbq.ConversationNote
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := q.GetConversationView(r.Context(), id); err != nil {
			return err
		}
		n, err = q.InsertNote(r.Context(), dbq.InsertNoteParams{
			ID: db.NewID(), TenantID: p.TenantID, ConversationID: id, AuthorUserID: p.UserID, Body: req.Body,
		})
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, Note{ID: n.ID, Body: n.Body, AuthorID: n.AuthorUserID, CreatedAt: n.CreatedAt})
	return nil
}

type QuickReply struct {
	ID       uuid.UUID `json:"id"`
	Shortcut string    `json:"shortcut"`
	Body     string    `json:"body"`
}

func (s *Service) quickReplies(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []QuickReply{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListQuickReplies(r.Context())
		for _, qr := range rows {
			out = append(out, QuickReply{ID: qr.ID, Shortcut: qr.Shortcut, Body: qr.Body})
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Service) addQuickReply(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Shortcut string `json:"shortcut"`
		Body     string `json:"body"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Shortcut = strings.TrimPrefix(strings.TrimSpace(req.Shortcut), "/")
	if req.Shortcut == "" || len(req.Shortcut) > 32 || strings.ContainsAny(req.Shortcut, " \t\n") {
		return httpx.BadRequest("shortcut", "A shortcut is one word of up to 32 characters.")
	}
	if strings.TrimSpace(req.Body) == "" || len(req.Body) > 4096 {
		return httpx.BadRequest("body", "A quick reply needs 1 to 4096 characters.")
	}
	var qr dbq.QuickReply
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		qr, err = q.InsertQuickReply(r.Context(), dbq.InsertQuickReplyParams{
			ID: db.NewID(), TenantID: p.TenantID, Shortcut: req.Shortcut, Body: req.Body, CreatedBy: &p.UserID,
		})
		return err
	})
	if db.IsUniqueViolation(err, "") {
		return httpx.NewError(http.StatusConflict, "conflict", "A quick reply with this shortcut already exists.")
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, QuickReply{ID: qr.ID, Shortcut: qr.Shortcut, Body: qr.Body})
	return nil
}

func (s *Service) deleteQuickReply(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var n int64
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		n, err = q.DeleteQuickReply(r.Context(), id)
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return httpx.ErrNotFound
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
