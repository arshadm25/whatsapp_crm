package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
)

// inviteTTL is how long an invite link works.
const inviteTTL = 7 * 24 * time.Hour

// TeamRoutes mounts /internal/team: members, invites and workspace settings. Owners and admins
// manage the team; only owners grant or remove the owner role and edit the workspace.
func (s *Service) TeamRoutes(r chi.Router) {
	r.Use(RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
	r.Get("/members", httpx.Handler(s.log, s.listTeam))
	r.Patch("/members/{id}", httpx.Handler(s.log, s.changeRole))
	r.Delete("/members/{id}", httpx.Handler(s.log, s.removeMember))
	r.Get("/invites", httpx.Handler(s.log, s.listInvites))
	r.Post("/invites", httpx.Handler(s.log, s.invite))
	r.Delete("/invites/{id}", httpx.Handler(s.log, s.revokeInvite))
	r.Post("/invites/{id}/resend", httpx.Handler(s.log, s.resendInvite))
	r.Get("/workspace", httpx.Handler(s.log, s.workspace))
	r.With(RequireRole(dbq.MemberRoleOwner)).Patch("/workspace", httpx.Handler(s.log, s.updateWorkspace))
	r.With(RequireRole(dbq.MemberRoleOwner)).Put("/workspace/two-factor", httpx.Handler(s.log, s.setRequireTwoFactor))
}

type Member struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	LastLoginAt *time.Time `json:"last_login_at"`
	JoinedAt    time.Time  `json:"joined_at"`
}

type Invite struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	InvitedByName string    `json:"invited_by_name"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
	// Link is returned only when the invite is created, so the inviter can also share it directly.
	Link string `json:"link,omitempty"`
}

// canManage reports whether actor may give or take away role. Admins manage agents and
// developers; owners manage everyone.
func canManage(actor, role dbq.MemberRole) bool {
	if actor == dbq.MemberRoleOwner {
		return true
	}
	return role == dbq.MemberRoleAgent || role == dbq.MemberRoleDeveloper
}

func parseRole(v string) (dbq.MemberRole, error) {
	r := dbq.MemberRole(v)
	if !r.Valid() {
		return r, httpx.BadRequest("role", "role must be owner, admin, agent or developer.")
	}
	return r, nil
}

func (s *Service) listTeam(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	out := []Member{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListTeam(r.Context())
		for _, m := range rows {
			out = append(out, Member{ID: m.ID, Name: m.Name, Email: m.Email, Role: string(m.Role), LastLoginAt: m.LastLoginAt, JoinedAt: m.CreatedAt})
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func memberID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return id, httpx.ErrNotFound
	}
	return id, nil
}

// checkTarget loads a member's role and refuses changes the actor may not make.
func checkTarget(r *http.Request, q *dbq.Queries, p Principal, id uuid.UUID) (dbq.MemberRole, error) {
	if id == p.UserID {
		return "", httpx.NewError(http.StatusConflict, "conflict", "You cannot change your own role or remove yourself. Ask another owner.")
	}
	role, err := q.GetMemberRole(r.Context(), id)
	if db.IsNotFound(err) {
		return role, httpx.ErrNotFound
	}
	if err != nil {
		return role, err
	}
	if !canManage(p.Role, role) {
		return role, httpx.NewError(http.StatusForbidden, "forbidden", "Only an owner can change owners and admins.")
	}
	return role, nil
}

func (s *Service) changeRole(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	id, err := memberID(r)
	if err != nil {
		return err
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	role, err := parseRole(req.Role)
	if err != nil {
		return err
	}
	ip, _ := clientInfo(r)
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := checkTarget(r, q, p, id); err != nil {
			return err
		}
		if !canManage(p.Role, role) {
			return httpx.NewError(http.StatusForbidden, "forbidden", "Only an owner can make someone an owner or admin.")
		}
		if err := q.SetMemberRole(r.Context(), dbq.SetMemberRoleParams{UserID: id, Role: role}); err != nil {
			return err
		}
		return audit(r.Context(), q, &p.TenantID, p.UserID, "member.role."+string(role), "user", id.String(), ip)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) removeMember(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	id, err := memberID(r)
	if err != nil {
		return err
	}
	ip, _ := clientInfo(r)
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := checkTarget(r, q, p, id); err != nil {
			return err
		}
		if err := q.DeleteMembership(r.Context(), id); err != nil {
			return err
		}
		if err := q.ClearSessionsForTenant(r.Context(), dbq.ClearSessionsForTenantParams{UserID: id, TenantID: &p.TenantID}); err != nil {
			return err
		}
		return audit(r.Context(), q, &p.TenantID, p.UserID, "member.remove", "user", id.String(), ip)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) listInvites(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	out := []Invite{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListOpenInvites(r.Context())
		for _, i := range rows {
			out = append(out, Invite{ID: i.ID, Email: i.Email, Role: string(i.Role), InvitedByName: i.InvitedByName,
				ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt})
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Service) invite(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	email, err := normaliseEmail(req.Email)
	if err != nil {
		return err
	}
	role, err := parseRole(req.Role)
	if err != nil {
		return err
	}
	if role == dbq.MemberRoleOwner {
		return httpx.BadRequest("role", "Invite as admin, agent or developer; an owner can make them an owner after they join.")
	}
	if !canManage(p.Role, role) {
		return httpx.NewError(http.StatusForbidden, "forbidden", "Only an owner can invite admins.")
	}
	token := randomToken()
	ip, _ := clientInfo(r)
	var (
		out                Invite
		inviter, workspace string
	)
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		team, err := q.ListTeam(r.Context())
		if err != nil {
			return err
		}
		for _, m := range team {
			if strings.EqualFold(m.Email, email) {
				return httpx.NewError(http.StatusConflict, "conflict", m.Name+" is already in this workspace.")
			}
			if m.ID == p.UserID {
				inviter = m.Name
			}
		}
		t, err := q.GetTenant(r.Context(), p.TenantID)
		if err != nil {
			return err
		}
		workspace = t.Name
		// Inviting the same email again replaces the earlier link.
		if err := q.DeleteOpenInvite(r.Context(), email); err != nil {
			return err
		}
		if err := seatRoom(r.Context(), q, true); err != nil {
			return err
		}
		inv, err := q.InsertInvite(r.Context(), dbq.InsertInviteParams{
			ID: db.NewID(), TenantID: p.TenantID, Email: email, Role: role, TokenHash: hashToken(token),
			InvitedBy: p.UserID, ExpiresAt: s.now().Add(inviteTTL),
		})
		if err != nil {
			return err
		}
		out = Invite{ID: inv.ID, Email: inv.Email, Role: string(inv.Role), InvitedByName: inviter, ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt}
		return audit(r.Context(), q, &p.TenantID, p.UserID, "invite.create", "invite", inv.ID.String(), ip)
	})
	if err != nil {
		return err
	}
	out.Link = s.cfg.PublicAppURL + "/invite?token=" + token
	s.sendInvite(r.Context(), out, inviter, workspace)
	httpx.JSON(w, http.StatusCreated, out)
	return nil
}

// sendInvite emails the invite link. A failure is logged: the invite stands, and the inviter can
// copy the link from the response instead.
func (s *Service) sendInvite(ctx context.Context, inv Invite, inviter, workspace string) {
	err := s.mailer.Send(ctx, mailer.Message{
		To:      inv.Email,
		Subject: fmt.Sprintf("%s invited you to %s on Ecogo WhatsApp", inviter, workspace),
		Text: fmt.Sprintf("Hi,\n\n%s invited you to join %s on Ecogo WhatsApp as %s.\n\nAccept the invite within 7 days:\n\n%s\n\n"+
			"If you were not expecting this, ignore this email.\n\nEcogo AI Technologies Pvt Ltd\n", inviter, workspace, inv.Role, inv.Link),
	})
	if err != nil {
		s.log.Warn("invite email not sent", "invite_id", inv.ID, "err", err)
	}
}

// resendInvite emails an open invite again with a new link, valid for another 7 days. The old
// link stops working.
func (s *Service) resendInvite(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	id, err := memberID(r)
	if err != nil {
		return err
	}
	token := randomToken()
	ip, _ := clientInfo(r)
	var (
		out                Invite
		inviter, workspace string
	)
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		inv, err := q.RefreshInvite(r.Context(), dbq.RefreshInviteParams{ID: id, TokenHash: hashToken(token), ExpiresAt: s.now().Add(inviteTTL)})
		if db.IsNotFound(err) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !canManage(p.Role, inv.Role) {
			return httpx.NewError(http.StatusForbidden, "forbidden", "Only an owner can invite admins.")
		}
		u, err := q.GetUserByID(r.Context(), p.UserID)
		if err != nil {
			return err
		}
		t, err := q.GetTenant(r.Context(), p.TenantID)
		if err != nil {
			return err
		}
		inviter, workspace = u.Name, t.Name
		out = Invite{ID: inv.ID, Email: inv.Email, Role: string(inv.Role), InvitedByName: inviter, ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt}
		return audit(r.Context(), q, &p.TenantID, p.UserID, "invite.resend", "invite", inv.ID.String(), ip)
	})
	if err != nil {
		return err
	}
	out.Link = s.cfg.PublicAppURL + "/invite?token=" + token
	s.sendInvite(r.Context(), out, inviter, workspace)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// setRequireTwoFactor turns the workspace's two-step verification requirement on or off. Members
// without it are asked to set it up before they can do anything else in the workspace.
func (s *Service) setRequireTwoFactor(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req struct {
		Required bool `json:"required"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if req.Required && !p.TOTPEnabled {
		return httpx.NewError(http.StatusConflict, "conflict", "Turn on two-step verification for your own account first, under Settings, Security.")
	}
	ip, _ := clientInfo(r)
	var t dbq.Tenant
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if err := q.SetRequireTwoFactor(r.Context(), dbq.SetRequireTwoFactorParams{ID: p.TenantID, RequireTwoFactor: req.Required}); err != nil {
			return err
		}
		action := "tenant.require_2fa_off"
		if req.Required {
			action = "tenant.require_2fa_on"
		}
		var err error
		if t, err = q.GetTenant(r.Context(), p.TenantID); err != nil {
			return err
		}
		return audit(r.Context(), q, &p.TenantID, p.UserID, action, "tenant", p.TenantID.String(), ip)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, workspaceOf(t))
	return nil
}

func (s *Service) revokeInvite(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	id, err := memberID(r)
	if err != nil {
		return err
	}
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		n, err := q.DeleteInvite(r.Context(), id)
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

type Workspace struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	LegalName *string   `json:"legal_name"`
	TimeZone  string    `json:"time_zone"`
	// MessageRetentionDays deletes messages and media older than this many days; null keeps them.
	MessageRetentionDays *int32 `json:"message_retention_days"`
	// RequireTwoFactor is read-only here; PUT /workspace/two-factor changes it.
	RequireTwoFactor bool `json:"require_two_factor"`
}

func workspaceOf(t dbq.Tenant) Workspace {
	return Workspace{ID: t.ID, Name: t.Name, LegalName: t.LegalName, TimeZone: t.Timezone,
		MessageRetentionDays: t.MessageRetentionDays, RequireTwoFactor: t.RequireTwoFactor}
}

func (s *Service) workspace(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var t dbq.Tenant
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		t, err = q.GetTenant(r.Context(), p.TenantID)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, workspaceOf(t))
	return nil
}

func (s *Service) updateWorkspace(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req Workspace
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 160 {
		return httpx.BadRequest("name", "Enter the business name.")
	}
	if req.LegalName != nil {
		if v := strings.TrimSpace(*req.LegalName); v == "" {
			req.LegalName = nil
		} else if utf8.RuneCountInString(v) > 200 {
			return httpx.BadRequest("legal_name", "The legal name must be at most 200 characters.")
		} else {
			req.LegalName = &v
		}
	}
	if _, err := time.LoadLocation(req.TimeZone); err != nil || req.TimeZone == "" || req.TimeZone == "Local" {
		return httpx.BadRequest("time_zone", "time_zone must be an IANA zone such as Asia/Kolkata.")
	}
	if d := req.MessageRetentionDays; d != nil && (*d < 30 || *d > 3650) {
		return httpx.BadRequest("message_retention_days", "Keep messages for 30 to 3650 days, or leave it empty to keep them.")
	}
	ip, _ := clientInfo(r)
	var t dbq.Tenant
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if t, err = q.UpdateTenantSettings(r.Context(), dbq.UpdateTenantSettingsParams{
			ID: p.TenantID, Name: req.Name, LegalName: req.LegalName, Timezone: req.TimeZone, MessageRetentionDays: req.MessageRetentionDays,
		}); err != nil {
			return err
		}
		return audit(r.Context(), q, &p.TenantID, p.UserID, "tenant.update", "tenant", p.TenantID.String(), ip)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, workspaceOf(t))
	return nil
}

// seatRoom refuses a new member or invite once the plan's seats (included plus paid extra) are
// all taken. A workspace without a plan, on its trial, has no seat limit. Run it inside the
// tenant; withInvites counts invitations that have not been accepted yet.
func seatRoom(ctx context.Context, q *dbq.Queries, withInvites bool) error {
	u, err := q.SeatUsage(ctx)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	used := u.Members
	if withInvites {
		used += u.OpenInvites
	}
	if used >= u.SeatLimit {
		return httpx.NewError(http.StatusConflict, "seat_limit", fmt.Sprintf(
			"Your plan has %d seat%s and all are taken. The workspace owner can add seats or choose a larger plan under Settings, Billing.",
			u.SeatLimit, map[bool]string{true: "", false: "s"}[u.SeatLimit == 1]))
	}
	return nil
}
