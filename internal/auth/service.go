// Package auth handles D1: sign-up (user + tenant + owner membership), log in and out, email
// verification, the session and CSRF middleware the dashboard uses, the team (members, roles,
// invites), workspace settings, and the user's own name and password.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
)

const verifyEmailPurpose = "verify_email"

type Service struct {
	db     *db.DB
	keys   *envelope.Keyring
	cfg    *config.Config
	mailer mailer.Mailer
	log    *slog.Logger
	now    func() time.Time
}

func NewService(d *db.DB, keys *envelope.Keyring, cfg *config.Config, m mailer.Mailer, log *slog.Logger) *Service {
	return &Service{db: d, keys: keys, cfg: cfg, mailer: m, log: log, now: time.Now}
}

// Routes mounts the /internal/auth endpoints. CSRF is applied by the caller.
func (s *Service) Routes(r chi.Router) {
	r.Post("/signup", httpx.Handler(s.log, s.signup))
	r.Post("/login", httpx.Handler(s.log, s.login))
	r.Post("/verify-email", httpx.Handler(s.log, s.verifyEmail))
	r.Get("/invites/{token}", httpx.Handler(s.log, s.inviteInfo))
	r.Post("/invites/accept", httpx.Handler(s.log, s.acceptInvite))
	r.Post("/password/forgot", httpx.Handler(s.log, s.forgotPassword))
	r.Post("/password/reset", httpx.Handler(s.log, s.resetPassword))
	r.Group(func(r chi.Router) {
		r.Use(s.RequireSession)
		r.Post("/logout", httpx.Handler(s.log, s.logout))
		r.Get("/me", httpx.Handler(s.log, s.me))
		r.Post("/resend-verification", httpx.Handler(s.log, s.resendVerification))
		r.Post("/switch-tenant", httpx.Handler(s.log, s.switchTenant))
		r.Patch("/profile", httpx.Handler(s.log, s.updateProfile))
		r.Post("/password", httpx.Handler(s.log, s.changePassword))
		r.Post("/2fa/setup", httpx.Handler(s.log, s.setupTOTP))
		r.Post("/2fa/enable", httpx.Handler(s.log, s.enableTOTP))
		r.Post("/2fa/disable", httpx.Handler(s.log, s.disableTOTP))
		r.Post("/2fa/verify", httpx.Handler(s.log, s.verifyTOTP))
	})
}

type signupRequest struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	BusinessName string `json:"business_name"`
}

func (s *Service) signup(w http.ResponseWriter, r *http.Request) error {
	var req signupRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	req.BusinessName = strings.TrimSpace(req.BusinessName)
	email, err := normaliseEmail(req.Email)
	if err != nil {
		return err
	}
	switch {
	case req.Name == "" || utf8.RuneCountInString(req.Name) > 120:
		return httpx.BadRequest("name", "Enter your name.")
	case req.BusinessName == "" || utf8.RuneCountInString(req.BusinessName) > 160:
		return httpx.BadRequest("business_name", "Enter your business name.")
	case utf8.RuneCountInString(req.Password) < 10 || len(req.Password) > 256:
		return httpx.BadRequest("password", "Use a password of at least 10 characters.")
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return err
	}

	userID, tenantID := db.NewID(), db.NewID()
	slug := slugify(req.BusinessName) + "-" + strings.ToLower(tenantID.String()[len(tenantID.String())-6:])
	token := randomToken()
	ip, ua := clientInfo(r)

	err = s.db.Global(r.Context(), func(q *dbq.Queries, tx pgx.Tx) error {
		if _, err := q.CreateUser(r.Context(), dbq.CreateUserParams{ID: userID, Email: email, Name: req.Name, PasswordHash: hash}); err != nil {
			if db.IsUniqueViolation(err, "users_email_key") {
				return httpx.NewError(http.StatusConflict, "conflict", "An account with this email already exists. Log in instead.")
			}
			return err
		}
		if _, err := q.CreateTenant(r.Context(), dbq.CreateTenantParams{ID: tenantID, Name: req.BusinessName, Slug: slug}); err != nil {
			return err
		}
		// memberships is tenant-scoped, so set the new tenant for the rest of this transaction.
		if err := db.SetTenant(r.Context(), tx, tenantID); err != nil {
			return err
		}
		if err := q.CreateMembership(r.Context(), dbq.CreateMembershipParams{TenantID: tenantID, UserID: userID, Role: dbq.MemberRoleOwner}); err != nil {
			return err
		}
		if _, err := q.CreateSession(r.Context(), dbq.CreateSessionParams{
			ID: db.NewID(), UserID: userID, TenantID: &tenantID, TokenHash: hashToken(token),
			Ip: ip, UserAgent: ua, ExpiresAt: s.now().Add(s.cfg.SessionTTL),
		}); err != nil {
			return err
		}
		return audit(r.Context(), q, &tenantID, userID, "tenant.create", "tenant", tenantID.String(), ip)
	})
	if err != nil {
		return err
	}
	s.sendVerification(r.Context(), userID, email, req.Name)
	s.setSessionCookie(w, token, false)
	return s.writeMe(w, r, userID, tenantID, http.StatusCreated)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Remember keeps the session for 30 days, across browser restarts ("Keep me logged in").
	Remember bool `json:"remember"`
}

var errBadLogin = httpx.NewError(http.StatusUnauthorized, "unauthorized", "Email or password is incorrect.")

func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	email, err := normaliseEmail(req.Email)
	if err != nil {
		return errBadLogin
	}
	token := randomToken()
	ip, ua := clientInfo(r)
	var userID, tenantID uuid.UUID
	var needsCode bool
	err = s.db.Global(r.Context(), func(q *dbq.Queries, tx pgx.Tx) error {
		u, err := q.GetUserByEmail(r.Context(), email)
		if db.IsNotFound(err) {
			_, _ = HashPassword(req.Password) // keep timing similar for unknown emails
			return errBadLogin
		}
		if err != nil {
			return err
		}
		ok, err := CheckPassword(u.PasswordHash, req.Password)
		if err != nil {
			return err
		}
		if !ok {
			return errBadLogin
		}
		userID = u.ID
		needsCode = u.TotpSecretEnc != nil
		ms, err := q.UserMemberships(r.Context(), u.ID)
		if err != nil {
			return err
		}
		var tenant *uuid.UUID
		for _, m := range ms {
			if m.TenantStatus == dbq.TenantStatusActive {
				tenantID = m.TenantID
				tenant = &tenantID
				break
			}
		}
		if _, err := q.CreateSession(r.Context(), dbq.CreateSessionParams{
			ID: db.NewID(), UserID: u.ID, TenantID: tenant, TokenHash: hashToken(token),
			Ip: ip, UserAgent: ua, ExpiresAt: s.now().Add(s.sessionTTL(req.Remember)), Persistent: req.Remember,
		}); err != nil {
			return err
		}
		if err := q.UpdateLastLogin(r.Context(), u.ID); err != nil {
			return err
		}
		return audit(r.Context(), q, tenant, u.ID, "user.login", "user", u.ID.String(), ip)
	})
	if err != nil {
		return err
	}
	s.setSessionCookie(w, token, req.Remember)
	if needsCode {
		httpx.JSON(w, http.StatusOK, map[string]any{"mfa_required": true})
		return nil
	}
	return s.writeMe(w, r, userID, tenantID, http.StatusOK)
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(SessionCookie)
	if err == nil {
		err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
			return q.DeleteSession(r.Context(), hashToken(c.Value))
		})
		if err != nil {
			return err
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	if p.MFAPending {
		// Until the code is entered, say only that it is needed.
		httpx.JSON(w, http.StatusOK, map[string]any{"mfa_required": true})
		return nil
	}
	return s.writeMe(w, r, p.UserID, p.TenantID, http.StatusOK)
}

type switchTenantRequest struct {
	TenantID uuid.UUID `json:"tenant_id"`
}

func (s *Service) switchTenant(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var req switchTenantRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		ms, err := q.UserMemberships(r.Context(), p.UserID)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if m.TenantID == req.TenantID && m.TenantStatus == dbq.TenantStatusActive {
				return q.SetSessionTenant(r.Context(), dbq.SetSessionTenantParams{ID: p.SessionID, TenantID: &req.TenantID})
			}
		}
		return httpx.ErrNotFound
	})
	if err != nil {
		return err
	}
	return s.writeMe(w, r, p.UserID, req.TenantID, http.StatusOK)
}

type verifyEmailRequest struct {
	Token string `json:"token"`
}

func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var req verifyEmailRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	userID, err := verifyToken(s.cfg.AppSecret, verifyEmailPurpose, req.Token, s.now())
	if err != nil {
		return httpx.BadRequest("token", "This verification link is invalid or has expired. Request a new one from the dashboard.")
	}
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		return q.MarkEmailVerified(r.Context(), userID)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) resendVerification(w http.ResponseWriter, r *http.Request) error {
	p, _ := PrincipalFrom(r.Context())
	var u dbq.User
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		u, err = q.GetUserByID(r.Context(), p.UserID)
		return err
	})
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt == nil {
		s.sendVerification(r.Context(), u.ID, u.Email, u.Name)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sendVerification emails a 48-hour verification link. Failures are logged, not returned:
// sign-up must not fail because the mail relay is down, and the user can resend.
func (s *Service) sendVerification(ctx context.Context, userID uuid.UUID, email, name string) {
	tok := signToken(s.cfg.AppSecret, verifyEmailPurpose, userID, s.now().Add(48*time.Hour))
	link := s.cfg.PublicAppURL + "/verify-email?token=" + tok
	err := s.mailer.Send(ctx, mailer.Message{
		To:      email,
		Subject: "Verify your email for Ecogo WhatsApp",
		Text: fmt.Sprintf("Hi %s,\n\nConfirm your email address by opening this link within 48 hours:\n\n%s\n\n"+
			"If you did not sign up for Ecogo WhatsApp, ignore this email.\n\nEcogo Software Solutions Pvt Ltd\n", name, link),
	})
	if err != nil {
		s.log.Warn("verification email not sent", "user_id", userID, "err", err)
	}
}

// MeResponse is what the dashboard needs to render its shell.
type MeResponse struct {
	User struct {
		ID               uuid.UUID `json:"id"`
		Email            string    `json:"email"`
		Name             string    `json:"name"`
		EmailVerified    bool      `json:"email_verified"`
		TwoFactorEnabled bool      `json:"two_factor_enabled"`
		PlatformAdmin    bool      `json:"is_platform_admin"`
	} `json:"user"`
	Tenant      *TenantInfo  `json:"tenant"`
	Memberships []TenantInfo `json:"memberships"`
	// TwoFactorSetupRequired is true when the workspace requires two-step verification and the
	// user has not turned it on; nothing else in the workspace works until they do.
	TwoFactorSetupRequired bool `json:"two_factor_setup_required"`
}

type TenantInfo struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
	Role string    `json:"role"`
}

func (s *Service) writeMe(w http.ResponseWriter, r *http.Request, userID, tenantID uuid.UUID, status int) error {
	var resp MeResponse
	resp.Memberships = []TenantInfo{}
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		u, err := q.GetUserByID(r.Context(), userID)
		if err != nil {
			return err
		}
		resp.User.ID, resp.User.Email, resp.User.Name = u.ID, u.Email, u.Name
		resp.User.EmailVerified = u.EmailVerifiedAt != nil
		resp.User.TwoFactorEnabled = u.TotpSecretEnc != nil
		resp.User.PlatformAdmin = u.IsPlatformAdmin
		ms, err := q.UserMemberships(r.Context(), userID)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if m.TenantStatus != dbq.TenantStatusActive {
				continue
			}
			ti := TenantInfo{ID: m.TenantID, Name: m.TenantName, Slug: m.TenantSlug, Role: string(m.Role)}
			resp.Memberships = append(resp.Memberships, ti)
			if m.TenantID == tenantID {
				resp.Tenant = &ti
			}
		}
		if resp.Tenant != nil && !resp.User.TwoFactorEnabled {
			resp.TwoFactorSetupRequired, err = q.TenantRequiresTwoFactor(r.Context(), tenantID)
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, status, resp)
	return nil
}

func audit(ctx context.Context, q *dbq.Queries, tenantID *uuid.UUID, userID uuid.UUID, action, targetType, targetID string, ip *netip.Addr) error {
	return q.InsertAuditLog(ctx, dbq.InsertAuditLogParams{
		TenantID: tenantID, ActorType: dbq.ActorTypeUser, ActorID: &userID, Action: action,
		TargetType: &targetType, TargetID: &targetID, Ip: ip, Metadata: json.RawMessage("{}"),
	})
}

func normaliseEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return "", httpx.BadRequest("email", "Enter a valid email address.")
	}
	return strings.ToLower(s), nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		return "workspace"
	}
	return slug
}

// clientInfo returns the caller's IP and user agent. Behind the cluster ingress, the
// controller sets X-Forwarded-For; RemoteAddr is used when it is absent.
func clientInfo(r *http.Request) (*netip.Addr, *string) {
	host := r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		host, _, _ = strings.Cut(xff, ",")
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	var ip *netip.Addr
	if a, err := netip.ParseAddr(strings.TrimSpace(host)); err == nil {
		ip = &a
	}
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return ip, &ua
}

// ClientIP is the caller's address, for audit entries written outside this package.
func ClientIP(r *http.Request) *netip.Addr {
	ip, _ := clientInfo(r)
	return ip
}
