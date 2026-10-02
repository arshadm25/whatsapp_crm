package auth

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// InviteInfo is what the invite page shows before the person accepts.
type InviteInfo struct {
	TenantName    string `json:"tenant_name"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	InvitedByName string `json:"invited_by_name"`
	// AccountExists tells the page to ask the person to log in rather than create an account.
	AccountExists bool `json:"account_exists"`
}

var errBadInvite = httpx.BadRequest("token", "This invite link is invalid, already used or expired. Ask for a new invite.")

// findInvite looks up an open, unexpired invite by its link token.
func (s *Service) findInvite(r *http.Request, q *dbq.Queries, token string) (dbq.InviteByTokenRow, error) {
	inv, err := q.InviteByToken(r.Context(), hashToken(token))
	if db.IsNotFound(err) || (err == nil && (inv.Accepted || s.now().After(inv.ExpiresAt))) {
		return inv, errBadInvite
	}
	return inv, err
}

func (s *Service) inviteInfo(w http.ResponseWriter, r *http.Request) error {
	var out InviteInfo
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		inv, err := s.findInvite(r, q, chi.URLParam(r, "token"))
		if err != nil {
			return err
		}
		out = InviteInfo{TenantName: inv.TenantName, Email: inv.Email, Role: string(inv.Role), InvitedByName: inv.InvitedByName}
		_, err = q.GetUserByEmail(r.Context(), inv.Email)
		out.AccountExists = err == nil
		if db.IsNotFound(err) {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type acceptRequest struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// acceptInvite adds the invited person to the workspace and opens it for them. Someone already
// logged in as the invited email joins with their account; someone new creates an account
// (their email is proven by the link); someone with an account who is logged out is asked to
// log in first.
func (s *Service) acceptInvite(w http.ResponseWriter, r *http.Request) error {
	var req acceptRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	var sess *dbq.Session
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
			got, err := q.GetSessionAuth(r.Context(), hashToken(c.Value))
			if err == nil && got.TotpEnabled && !got.Session.MfaPassed {
				return httpx.NewError(http.StatusForbidden, "mfa_required", "Enter the code from your authenticator app first.")
			}
			if err == nil {
				sess = &got.Session
			}
			if db.IsNotFound(err) {
				return nil
			}
			return err
		})
		if err != nil {
			return err
		}
	}

	token := randomToken()
	ip, ua := clientInfo(r)
	var userID, tenantID uuid.UUID
	err := s.db.Global(r.Context(), func(q *dbq.Queries, tx pgx.Tx) error {
		inv, err := s.findInvite(r, q, req.Token)
		if err != nil {
			return err
		}
		tenantID = inv.TenantID
		existing, err := q.GetUserByEmail(r.Context(), inv.Email)
		switch {
		case err == nil && sess != nil && sess.UserID == existing.ID:
			userID = existing.ID
		case err == nil && sess != nil:
			return httpx.NewError(http.StatusForbidden, "forbidden", "This invite is for "+inv.Email+". Log out, then open the link again.")
		case err == nil:
			return httpx.NewError(http.StatusUnauthorized, "login_required", "You already have an account. Log in as "+inv.Email+" to accept.")
		case !db.IsNotFound(err):
			return err
		default:
			name := strings.TrimSpace(req.Name)
			if name == "" || utf8.RuneCountInString(name) > 120 {
				return httpx.BadRequest("name", "Enter your name.")
			}
			if utf8.RuneCountInString(req.Password) < 10 || len(req.Password) > 256 {
				return httpx.BadRequest("password", "Use a password of at least 10 characters.")
			}
			hash, err := HashPassword(req.Password)
			if err != nil {
				return err
			}
			u, err := q.CreateVerifiedUser(r.Context(), dbq.CreateVerifiedUserParams{ID: db.NewID(), Email: inv.Email, Name: name, PasswordHash: hash})
			if err != nil {
				return err
			}
			userID = u.ID
		}

		if err := db.SetTenant(r.Context(), tx, tenantID); err != nil {
			return err
		}
		if n, err := q.MarkInviteAccepted(r.Context(), inv.ID); err != nil || n == 0 {
			if err == nil {
				err = errBadInvite
			}
			return err
		}
		if _, err := q.GetMemberRole(r.Context(), userID); db.IsNotFound(err) {
			if err := q.CreateMembership(r.Context(), dbq.CreateMembershipParams{TenantID: tenantID, UserID: userID, Role: inv.Role}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if sess != nil {
			if err := q.SetSessionTenant(r.Context(), dbq.SetSessionTenantParams{ID: sess.ID, TenantID: &tenantID}); err != nil {
				return err
			}
		} else if _, err := q.CreateSession(r.Context(), dbq.CreateSessionParams{
			ID: db.NewID(), UserID: userID, TenantID: &tenantID, TokenHash: hashToken(token),
			Ip: ip, UserAgent: ua, ExpiresAt: s.now().Add(s.cfg.SessionTTL),
		}); err != nil {
			return err
		}
		return audit(r.Context(), q, &tenantID, userID, "invite.accept", "invite", inv.ID.String(), ip)
	})
	if err != nil {
		return err
	}
	if sess == nil {
		s.setSessionCookie(w, token)
	}
	return s.writeMe(w, r, userID, tenantID, http.StatusOK)
}

// updateProfile changes the signed-in user's display name.
func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return httpx.BadRequest("name", "Enter your name.")
	}
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		return q.UpdateUserName(r.Context(), dbq.UpdateUserNameParams{ID: p.UserID, Name: name})
	})
	if err != nil {
		return err
	}
	return s.writeMe(w, r, p.UserID, p.TenantID, http.StatusOK)
}

// changePassword checks the current password, sets the new one and logs out every other session.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if utf8.RuneCountInString(req.NewPassword) < 10 || len(req.NewPassword) > 256 {
		return httpx.BadRequest("new_password", "Use a password of at least 10 characters.")
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	ip, _ := clientInfo(r)
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		u, err := q.GetUserByID(r.Context(), p.UserID)
		if err != nil {
			return err
		}
		ok, err := CheckPassword(u.PasswordHash, req.CurrentPassword)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.BadRequest("current_password", "The current password is incorrect.")
		}
		if err := q.SetPassword(r.Context(), dbq.SetPasswordParams{ID: p.UserID, PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.DeleteOtherSessions(r.Context(), dbq.DeleteOtherSessionsParams{UserID: p.UserID, KeepID: p.SessionID}); err != nil {
			return err
		}
		var tenant *uuid.UUID
		if p.TenantID != uuid.Nil {
			tenant = &p.TenantID
		}
		return audit(r.Context(), q, tenant, p.UserID, "user.password_change", "user", p.UserID.String(), ip)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
