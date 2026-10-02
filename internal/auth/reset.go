package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
)

// resetTTL is how long a password reset link works.
const resetTTL = time.Hour

// resetPurpose binds a reset link to the password it replaces, so the link stops working once
// it has been used (or the password changed some other way).
func resetPurpose(passwordHash string) string {
	h := sha256.Sum256([]byte(passwordHash))
	return "reset_password:" + hex.EncodeToString(h[:8])
}

// tokenSubject reads the user ID from a signed token without checking it; verifyToken checks it.
func tokenSubject(token string) (uuid.UUID, bool) {
	p64, _, _ := strings.Cut(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(p64)
	if err != nil {
		return uuid.Nil, false
	}
	parts := strings.Split(string(payload), ".")
	if len(parts) != 3 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[1])
	return id, err == nil
}

// forgotPassword emails a reset link. It answers the same whether or not the email has an
// account, so it cannot be used to find out who has one.
func (s *Service) forgotPassword(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	email, err := normaliseEmail(req.Email)
	if err != nil {
		return err
	}
	var u dbq.User
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		u, err = q.GetUserByEmail(r.Context(), email)
		return err
	})
	if err != nil && !db.IsNotFound(err) {
		return err
	}
	if err == nil {
		tok := signToken(s.cfg.AppSecret, resetPurpose(u.PasswordHash), u.ID, s.now().Add(resetTTL))
		link := s.cfg.PublicAppURL + "/reset-password?token=" + tok
		err := s.mailer.Send(r.Context(), mailer.Message{
			To:      u.Email,
			Subject: "Reset your Ecogo WhatsApp password",
			Text: fmt.Sprintf("Hi %s,\n\nSomeone asked to reset the password for your Ecogo WhatsApp account. "+
				"To choose a new password, open this link within an hour:\n\n%s\n\n"+
				"If it was not you, ignore this email; your password stays the same.\n\nEcogo Software Solutions Pvt Ltd\n", u.Name, link),
		})
		if err != nil {
			s.log.Warn("password reset email not sent", "user_id", u.ID, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

var errResetLink = httpx.BadRequest("token", "This reset link is invalid, used or expired. Ask for a new one.")

// resetPassword sets a new password from a reset link and logs the user out everywhere.
func (s *Service) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if utf8.RuneCountInString(req.Password) < 10 || len(req.Password) > 256 {
		return httpx.BadRequest("password", "Use a password of at least 10 characters.")
	}
	userID, ok := tokenSubject(req.Token)
	if !ok {
		return errResetLink
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return err
	}
	ip, _ := clientInfo(r)
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		u, err := q.GetUserByID(r.Context(), userID)
		if db.IsNotFound(err) {
			return errResetLink
		}
		if err != nil {
			return err
		}
		if _, err := verifyToken(s.cfg.AppSecret, resetPurpose(u.PasswordHash), req.Token, s.now()); err != nil {
			return errResetLink
		}
		if err := q.SetPassword(r.Context(), dbq.SetPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
			return err
		}
		// Whoever knew the old password is logged out too.
		if err := q.DeleteUserSessions(r.Context(), u.ID); err != nil {
			return err
		}
		// Opening the emailed link proves the address.
		if u.EmailVerifiedAt == nil {
			if err := q.MarkEmailVerified(r.Context(), u.ID); err != nil {
				return err
			}
		}
		return audit(r.Context(), q, nil, u.ID, "user.password_reset", "user", u.ID.String(), ip)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
