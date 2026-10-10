// Package onboarding implements D2, Connect WhatsApp through Embedded Signup v4, for both
// new numbers (standard) and numbers already on the WhatsApp Business app (coexistence).
//
// The dashboard opens Meta's popup and posts the result here. The api exchanges the short-lived
// code for a Business Integration System User token right away, stores it envelope-encrypted, and
// enqueues an onboarding job; the worker does the remaining Graph calls one step at a time and
// records each step in onboarding_sessions, so a failed step can be retried on its own.
package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

// Meta is the part of metaclient the onboarding flow uses; tests substitute a fake server.
type Meta interface {
	ExchangeCode(ctx context.Context, code string) (string, error)
	GetWABA(ctx context.Context, token, wabaID string) (*metaclient.WABA, error)
	SubscribeApp(ctx context.Context, token, wabaID string) error
	SharedWABAs(ctx context.Context, token string) ([]string, error)
	ListPhoneNumbers(ctx context.Context, token, wabaID string) ([]metaclient.PhoneNumber, error)
	GetPhoneNumber(ctx context.Context, token, phoneNumberID string) (*metaclient.PhoneNumber, error)
	RegisterPhoneNumber(ctx context.Context, token, phoneNumberID, pin string) error
	RequestSMBAppData(ctx context.Context, token, phoneNumberID, syncType string) error
}

type Service struct {
	db   *db.DB
	keys *envelope.Keyring
	meta Meta
	jobs jobs.Inserter
	log  *slog.Logger
}

func NewService(d *db.DB, keys *envelope.Keyring, meta Meta, inserter jobs.Inserter, log *slog.Logger) *Service {
	return &Service{db: d, keys: keys, meta: meta, jobs: inserter, log: log}
}

// Routes mounts /internal/onboarding. Callers apply session, tenant and CSRF middleware.
func (s *Service) Routes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner, dbq.MemberRoleAdmin))
	r.Post("/sessions", httpx.Handler(s.log, s.start))
	r.Get("/sessions/{id}", httpx.Handler(s.log, s.get))
	r.Post("/sessions/{id}/complete", httpx.Handler(s.log, s.complete))
	r.Post("/sessions/{id}/complete-with-token", httpx.Handler(s.log, s.completeWithToken))
	r.Post("/sessions/{id}/cancel", httpx.Handler(s.log, s.cancel))
	r.Post("/sessions/{id}/retry", httpx.Handler(s.log, s.retry))
}

type startRequest struct {
	Flow string `json:"flow"` // standard or coexistence
}

func (s *Service) start(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req startRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	flow := dbq.OnboardingFlow(req.Flow)
	if !flow.Valid() {
		return httpx.BadRequest("flow", "flow must be standard or coexistence.")
	}
	var sess dbq.OnboardingSession
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		// Fail before the owner goes through Meta's popup when the plan has no room.
		if err := billing.NumberRoom(r.Context(), q, ""); err != nil {
			return err
		}
		var err error
		sess, err = q.CreateOnboardingSession(r.Context(), dbq.CreateOnboardingSessionParams{
			ID: db.NewID(), TenantID: p.TenantID, UserID: p.UserID, Flow: flow,
		})
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, toView(sess))
	return nil
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var sess dbq.OnboardingSession
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		sess, err = q.GetOnboardingSession(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, toView(sess))
	return nil
}

// completeRequest carries what the Embedded Signup popup returned: the authorization code from
// FB.login and the IDs from the WA_EMBEDDED_SIGNUP session event.
type completeRequest struct {
	Code          string `json:"code"`
	WabaID        string `json:"waba_id"`
	PhoneNumberID string `json:"phone_number_id"`
	BusinessID    string `json:"business_id"`
}

var metaID = regexp.MustCompile(`^[0-9]{1,32}$`)

func (s *Service) complete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req completeRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" || len(req.Code) > 2048 {
		return httpx.BadRequest("code", "The authorization code from Meta is missing.")
	}
	exchange := func(ctx context.Context) (string, error) {
		token, err := s.meta.ExchangeCode(ctx, req.Code)
		if err != nil {
			code, msg := describe(err)
			return "", &connectError{code: code, log: "Meta did not accept the sign-up: " + msg + " Start the connection again.",
				err: httpx.NewError(http.StatusBadGateway, "meta_error", "Meta did not accept the sign-up. Start the connection again.")}
		}
		return token, nil
	}
	if req.WabaID != "" {
		// Codes expire within minutes, so the exchange cannot wait for the worker.
		return s.connect(w, r, id, req, exchange)
	}

	// The popup's session event did not reach the dashboard, so only the code came back. Exchange
	// it now and ask Meta which WABA and number were shared.
	ctx := metaclient.WithTenant(r.Context(), principalTenant(r))
	token, err := exchange(ctx)
	if err == nil {
		req.WabaID, req.PhoneNumberID, err = s.resolveShared(ctx, token)
	}
	if err != nil {
		var ce *connectError
		if errors.As(err, &ce) {
			p, _ := auth.PrincipalFrom(r.Context())
			s.recordFailure(r.Context(), p.TenantID, id, ce.code, ce.log)
			return ce.err
		}
		return err
	}
	return s.connect(w, r, id, req, func(context.Context) (string, error) { return token, nil })
}

func principalTenant(r *http.Request) string {
	p, _ := auth.PrincipalFrom(r.Context())
	return p.TenantID.String()
}

// resolveShared finds the one WABA the sign-up shared with our app and, when it has exactly one
// number, that number.
func (s *Service) resolveShared(ctx context.Context, token string) (wabaID, phoneNumberID string, err error) {
	fail := func(log, user string) (string, string, error) {
		return "", "", &connectError{code: "waba_not_found", log: log, err: httpx.NewError(http.StatusBadGateway, "meta_error", user)}
	}
	ids, err := s.meta.SharedWABAs(ctx, token)
	if err != nil {
		_, msg := describe(err)
		return fail("Could not read which WhatsApp Business Account was shared: "+msg,
			"Meta did not say which WhatsApp Business Account you chose. Start the connection again.")
	}
	if len(ids) != 1 {
		return fail(fmt.Sprintf("The sign-up shared %d WhatsApp Business Accounts; expected one.", len(ids)),
			"Meta did not say which WhatsApp Business Account you chose. Start the connection again and pick one account.")
	}
	numbers, err := s.meta.ListPhoneNumbers(ctx, token, ids[0])
	if err != nil {
		_, msg := describe(err)
		return fail("Could not list the numbers of WhatsApp Business Account "+ids[0]+": "+msg,
			"Meta did not return the number you added. Start the connection again.")
	}
	if len(numbers) == 1 {
		phoneNumberID = numbers[0].ID
	}
	return ids[0], phoneNumberID, nil
}

// tokenRequest connects a number in the workspace's own Meta business without Embedded Signup,
// using a system user access token the owner generated for our app. It is the way to connect
// numbers (Meta's test number included) before App Review grants Advanced Access, which
// Embedded Signup needs.
type tokenRequest struct {
	AccessToken   string `json:"access_token"`
	WabaID        string `json:"waba_id"`
	PhoneNumberID string `json:"phone_number_id"`
}

func (s *Service) completeWithToken(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req tokenRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.AccessToken = strings.TrimSpace(req.AccessToken)
	if req.AccessToken == "" || len(req.AccessToken) > 2048 || strings.ContainsAny(req.AccessToken, " \t\r\n") {
		return httpx.BadRequest("access_token", "Paste the system user access token from Meta Business Settings.")
	}
	if req.PhoneNumberID == "" {
		return httpx.BadRequest("phone_number_id", "Enter the phone number ID shown in Meta's WhatsApp API Setup.")
	}
	// Check the token reaches the WABA before storing it, so a wrong token or ID fails here.
	return s.connect(w, r, id, completeRequest{WabaID: req.WabaID, PhoneNumberID: req.PhoneNumberID}, func(ctx context.Context) (string, error) {
		if _, err := s.meta.GetWABA(ctx, req.AccessToken, req.WabaID); err != nil {
			_, msg := describe(err)
			return "", &connectError{code: "token_rejected", log: "Meta did not accept the access token for this WhatsApp Business Account: " + msg,
				err: httpx.NewError(http.StatusBadRequest, "token_rejected",
					"Meta did not accept the access token for this WhatsApp Business Account. Check that the system user has the account assigned and both WhatsApp permissions.")}
		}
		return req.AccessToken, nil
	})
}

// connectError is a token step failure: code and log are recorded on the session, err goes to the caller.
type connectError struct {
	code, log string
	err       error
}

func (e *connectError) Error() string { return e.log }

// connect records the IDs Meta returned, gets the access token, stores it encrypted and enqueues
// the rest of the onboarding.
func (s *Service) connect(w http.ResponseWriter, r *http.Request, id uuid.UUID, req completeRequest, getToken func(context.Context) (string, error)) error {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	switch {
	case !metaID.MatchString(req.WabaID):
		return httpx.BadRequest("waba_id", "The WhatsApp Business Account ID from Meta is missing.")
	case req.PhoneNumberID != "" && !metaID.MatchString(req.PhoneNumberID):
		return httpx.BadRequest("phone_number_id", "The phone number ID from Meta is not valid.")
	case req.BusinessID != "" && !metaID.MatchString(req.BusinessID):
		return httpx.BadRequest("business_id", "The business ID from Meta is not valid.")
	}

	// Step 1: check the session and that the WABA and number are not another workspace's,
	// then record the IDs so the session shows what Meta returned even if the exchange fails.
	var sess dbq.OnboardingSession
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		sess, err = q.GetOnboardingSessionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if sess.Step != dbq.OnboardingStepStarted && sess.Step != dbq.OnboardingStepCodeReceived {
			return httpx.NewError(http.StatusConflict, "conflict", "This connection attempt has already finished. Start a new one.")
		}
		if sess.Flow == dbq.OnboardingFlowStandard && req.PhoneNumberID == "" {
			return httpx.BadRequest("phone_number_id", "Meta did not return a phone number. Finish adding a number in the popup.")
		}
		if owner, err := q.WabaOwner(ctx, req.WabaID); err != nil {
			return err
		} else if owner != uuid.Nil && owner != p.TenantID {
			return errAlreadyConnected
		}
		if req.PhoneNumberID != "" {
			if owner, err := q.PhoneNumberOwner(ctx, req.PhoneNumberID); err != nil {
				return err
			} else if owner != uuid.Nil && owner != p.TenantID {
				return errAlreadyConnected
			}
		}
		return q.SetOnboardingCode(ctx, dbq.SetOnboardingCodeParams{
			ID: id, WabaID: &req.WabaID, PhoneNumberID: nonEmpty(req.PhoneNumberID), BusinessID: nonEmpty(req.BusinessID),
		})
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}

	// Step 2: get the token.
	token, err := getToken(metaclient.WithTenant(ctx, p.TenantID.String()))
	if err != nil {
		var ce *connectError
		if errors.As(err, &ce) {
			s.recordFailure(ctx, p.TenantID, id, ce.code, ce.log)
			return ce.err
		}
		return err
	}

	// Step 3: store the token encrypted, create the account, and enqueue the rest, in one transaction.
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		businessID := req.BusinessID
		acct, err := q.UpsertWhatsAppAccount(ctx, dbq.UpsertWhatsAppAccountParams{
			ID: db.NewID(), TenantID: p.TenantID, WabaID: req.WabaID, BusinessID: businessID, OnboardingFlow: sess.Flow,
		})
		if err != nil {
			return err
		}
		if acct.TenantID != p.TenantID { // the RLS check would also refuse this
			return errAlreadyConnected
		}
		if err := q.DeactivateCredentials(ctx, acct.ID); err != nil {
			return err
		}
		sealed, err := s.keys.Seal([]byte(token), credentialAAD(p.TenantID, acct.ID))
		if err != nil {
			return err
		}
		if err := q.InsertCredential(ctx, dbq.InsertCredentialParams{
			ID: db.NewID(), TenantID: p.TenantID, WhatsappAccountID: acct.ID,
			TokenCiphertext: sealed.Ciphertext, DataKeyCiphertext: sealed.DataKeyEncrypted,
			MasterKeyVersion: int32(sealed.MasterKeyVersion),
			Scopes:           []string{"whatsapp_business_management", "whatsapp_business_messaging"},
		}); err != nil {
			return err
		}
		if err := q.SetOnboardingStep(ctx, dbq.SetOnboardingStepParams{ID: id, Step: dbq.OnboardingStepTokenExchanged}); err != nil {
			return err
		}
		_, err = s.jobs.InsertTx(ctx, tx, Args{SessionID: id, TenantID: p.TenantID}, nil)
		return err
	})
	clear([]byte(token))
	if err != nil {
		return err
	}
	return s.writeSession(w, r, p.TenantID, id)
}

type cancelRequest struct {
	// Step is the screen Meta reported the popup closed on (CANCEL event current_step), if any.
	Step  string `json:"step"`
	Error string `json:"error"`
}

func (s *Service) cancel(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req cancelRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	code := "popup_closed"
	msg := "The Meta sign-up window was closed before it finished."
	if req.Step != "" {
		msg = fmt.Sprintf("The Meta sign-up window was closed at step %q.", truncate(req.Step, 64))
	}
	if req.Error != "" {
		code, msg = "popup_error", "Meta reported an error in the sign-up window: "+truncate(req.Error, 300)
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.CancelOnboardingSession(ctx, dbq.CancelOnboardingSessionParams{ID: id, ErrorCode: &code, ErrorMessage: &msg})
	})
	if err != nil {
		return err
	}
	return s.writeSession(w, r, p.TenantID, id)
}

// retry re-enqueues a session whose worker steps failed. A failed code exchange cannot be
// retried (the code is single-use), so those sessions must start over.
func (s *Service) retry(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		sess, err := q.GetOnboardingSessionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		switch sess.Step {
		case dbq.OnboardingStepStarted, dbq.OnboardingStepCodeReceived, dbq.OnboardingStepCancelled:
			return httpx.NewError(http.StatusConflict, "conflict", "This attempt cannot be resumed. Start the connection again.")
		case dbq.OnboardingStepCompleted:
			return httpx.NewError(http.StatusConflict, "conflict", "This number is already connected.")
		}
		if sess.ErrorMessage == nil {
			return httpx.NewError(http.StatusConflict, "conflict", "This connection is still in progress.")
		}
		if err := q.SetOnboardingStep(ctx, dbq.SetOnboardingStepParams{ID: id, Step: sess.Step}); err != nil {
			return err
		}
		_, err = s.jobs.InsertTx(ctx, tx, Args{SessionID: id, TenantID: p.TenantID}, nil)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.writeSession(w, r, p.TenantID, id)
}

func (s *Service) writeSession(w http.ResponseWriter, r *http.Request, tenantID, id uuid.UUID) error {
	var sess dbq.OnboardingSession
	err := s.db.InTenant(r.Context(), tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		sess, err = q.GetOnboardingSession(r.Context(), id)
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, toView(sess))
	return nil
}

func (s *Service) recordFailure(ctx context.Context, tenantID, id uuid.UUID, code, msg string) {
	ctx = context.WithoutCancel(ctx)
	err := s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.FailOnboardingSession(ctx, dbq.FailOnboardingSessionParams{ID: id, ErrorCode: &code, ErrorMessage: &msg})
	})
	if err != nil {
		s.log.Error("onboarding: record failure", "session_id", id, "err", err)
	}
}

var errAlreadyConnected = httpx.NewError(http.StatusConflict, "conflict",
	"This WhatsApp account or number is already connected to another Ecogo workspace.")

// SessionView is the API shape of an onboarding session.
type SessionView struct {
	ID            uuid.UUID  `json:"id"`
	Flow          string     `json:"flow"`
	Step          string     `json:"step"`
	State         string     `json:"state"` // awaiting_signup, in_progress, failed, completed, cancelled
	WabaID        *string    `json:"waba_id"`
	PhoneNumberID *string    `json:"phone_number_id"`
	Error         *ViewError `json:"error"`
	Attempts      int32      `json:"attempts"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	// StepTimes is when the session first reached each step.
	StepTimes map[string]time.Time `json:"step_times"`
}

type ViewError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func toView(s dbq.OnboardingSession) SessionView {
	v := SessionView{
		ID: s.ID, Flow: string(s.Flow), Step: string(s.Step), WabaID: s.WabaID, PhoneNumberID: s.PhoneNumberID,
		Attempts: s.Attempts, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, StepTimes: map[string]time.Time{},
	}
	_ = json.Unmarshal(s.StepTimes, &v.StepTimes)
	if s.ErrorMessage != nil {
		v.Error = &ViewError{Message: *s.ErrorMessage}
		if s.ErrorCode != nil {
			v.Error.Code = *s.ErrorCode
		}
	}
	switch {
	case s.Step == dbq.OnboardingStepCompleted:
		v.State = "completed"
	case s.Step == dbq.OnboardingStepCancelled:
		v.State = "cancelled"
	case s.ErrorMessage != nil || s.Step == dbq.OnboardingStepFailed:
		v.State = "failed"
	case s.Step == dbq.OnboardingStepStarted:
		v.State = "awaiting_signup"
	default:
		v.State = "in_progress"
	}
	return v
}

// describe turns an error into a stored code and a message safe to show the client.
func describe(err error) (string, string) {
	var me *metaclient.Error
	if errors.As(err, &me) {
		if hint, ok := metaHints[me.Code]; ok {
			return fmt.Sprintf("meta_%d", me.Code), hint
		}
		return fmt.Sprintf("meta_%d", me.Code), me.Friendly()
	}
	return "internal", "An unexpected error occurred."
}

// metaHints replace Meta's text for errors a client can fix themselves.
var metaHints = map[int]string{
	190:    "Meta's access token is no longer valid.",
	133005: "This number has a two-step verification PIN set by someone else. Turn off two-step verification for the number in WhatsApp Manager, then retry.",
	133010: "This phone number is not yet registered with WhatsApp Business. Finish verifying it in the Meta popup.",
	133016: "This number was registered too many times recently. Meta allows a retry later.",
}

func credentialAAD(tenantID, accountID uuid.UUID) []byte { return credentials.AAD(tenantID, accountID) }

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Args is the River job that runs the post-exchange onboarding steps.
type Args struct {
	SessionID uuid.UUID `json:"session_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
}

func (Args) Kind() string { return "onboarding" }

func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       jobs.QueueOnboarding,
		MaxAttempts: 5,
		// One live job per session: a double-clicked retry does not run the steps twice.
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled,
		}},
	}
}
