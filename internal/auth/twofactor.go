package auth

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// The TOTP seed is envelope-encrypted, bound to its user.
func totpAAD(userID uuid.UUID) []byte { return []byte("users:" + userID.String() + ":totp") }

var errBadCode = httpx.BadRequest("code", "That code is not right. Check the time on your phone and try the newest code.")

// TOTPSetup is a new secret for the user to add to an authenticator app. Nothing is stored
// until they prove it works with /2fa/enable.
type TOTPSetup struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

func (s *Service) setupTOTP(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	if p.TOTPEnabled {
		return httpx.NewError(http.StatusConflict, "conflict", "Two-step verification is already on.")
	}
	var u dbq.User
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		u, err = q.GetUserByID(r.Context(), p.UserID)
		return err
	})
	if err != nil {
		return err
	}
	secret := NewTOTPSecret()
	httpx.JSON(w, http.StatusOK, TOTPSetup{Secret: secret, URI: TOTPURI(secret, u.Email)})
	return nil
}

func (s *Service) enableTOTP(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if p.TOTPEnabled {
		return httpx.NewError(http.StatusConflict, "conflict", "Two-step verification is already on.")
	}
	if !CheckTOTP(req.Secret, req.Code, s.now()) {
		return errBadCode
	}
	sealed, err := s.keys.SealCompact([]byte(req.Secret), totpAAD(p.UserID))
	if err != nil {
		return err
	}
	ip, _ := clientInfo(r)
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		if err := q.SetTOTPSecret(r.Context(), dbq.SetTOTPSecretParams{ID: p.UserID, TotpSecretEnc: sealed}); err != nil {
			return err
		}
		// This session just proved the code; the user's other sessions ask for it next time.
		if err := q.SetSessionMFAPassed(r.Context(), p.SessionID); err != nil {
			return err
		}
		return audit(r.Context(), q, tenantOf(p), p.UserID, "user.2fa_enable", "user", p.UserID.String(), ip)
	})
	if err != nil {
		return err
	}
	return s.writeMe(w, r, p.UserID, p.TenantID, http.StatusOK)
}

// userSecret opens the user's TOTP seed; empty when two-step verification is off.
func (s *Service) userSecret(r *http.Request, q *dbq.Queries, userID uuid.UUID) (string, error) {
	u, err := q.GetUserByID(r.Context(), userID)
	if err != nil || u.TotpSecretEnc == nil {
		return "", err
	}
	plain, err := s.keys.OpenCompact(u.TotpSecretEnc, totpAAD(userID))
	return string(plain), err
}

func (s *Service) disableTOTP(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if p.PlatformAdmin {
		return httpx.NewError(http.StatusConflict, "conflict", "Platform admins must keep two-step verification on.")
	}
	ip, _ := clientInfo(r)
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		secret, err := s.userSecret(r, q, p.UserID)
		if err != nil {
			return err
		}
		if secret == "" {
			return httpx.NewError(http.StatusConflict, "conflict", "Two-step verification is already off.")
		}
		if !CheckTOTP(secret, req.Code, s.now()) {
			return errBadCode
		}
		if err := q.SetTOTPSecret(r.Context(), dbq.SetTOTPSecretParams{ID: p.UserID}); err != nil {
			return err
		}
		return audit(r.Context(), q, tenantOf(p), p.UserID, "user.2fa_disable", "user", p.UserID.String(), ip)
	})
	if err != nil {
		return err
	}
	return s.writeMe(w, r, p.UserID, p.TenantID, http.StatusOK)
}

// Six digits are guessable given enough tries, so a user gets maxCodeFailures wrong codes
// per codeWindow across all their sessions.
const (
	maxCodeFailures = 5
	codeWindow      = 15 * time.Minute
)

var errTooManyCodes = httpx.NewError(http.StatusTooManyRequests, "rate_limited", "Too many wrong codes. Wait 15 minutes and try again.")

// verifyTOTP completes a login for a user with two-step verification on.
func (s *Service) verifyTOTP(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	ip, _ := clientInfo(r)
	var wrong bool
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		n, err := q.CountRecentAudit(r.Context(), dbq.CountRecentAuditParams{
			ActorID: &p.UserID, Action: "user.2fa_failed", OccurredAt: s.now().Add(-codeWindow)})
		if err != nil {
			return err
		}
		if n >= maxCodeFailures {
			return errTooManyCodes
		}
		secret, err := s.userSecret(r, q, p.UserID)
		if err != nil {
			return err
		}
		if secret == "" || !CheckTOTP(secret, req.Code, s.now()) {
			wrong = true
			return audit(r.Context(), q, tenantOf(p), p.UserID, "user.2fa_failed", "user", p.UserID.String(), ip)
		}
		if err := q.SetSessionMFAPassed(r.Context(), p.SessionID); err != nil {
			return err
		}
		return audit(r.Context(), q, tenantOf(p), p.UserID, "user.2fa_passed", "user", p.UserID.String(), ip)
	})
	if err != nil {
		return err
	}
	if wrong {
		return errBadCode
	}
	return s.writeMe(w, r, p.UserID, p.TenantID, http.StatusOK)
}

func tenantOf(p Principal) *uuid.UUID {
	if p.TenantID == uuid.Nil {
		return nil
	}
	return &p.TenantID
}
