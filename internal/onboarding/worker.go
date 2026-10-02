package onboarding

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

// Worker runs the onboarding steps after the token exchange. Each step is idempotent and is
// committed before the next starts, so a retry resumes from the step that failed:
//
//	token_exchanged -> webhooks_subscribed -> number_registered            -> details_synced -> (template sync) completed
//	                                       -> contacts_sync_requested -> history_sync_requested (coexistence)
type Worker struct {
	river.WorkerDefaults[Args]
	db        *db.DB
	keys      *envelope.Keyring
	meta      Meta
	templates TemplateSyncer
	log       *slog.Logger
}

// TemplateSyncer copies an account's existing templates from Meta (internal/templates).
type TemplateSyncer interface {
	SyncAccount(ctx context.Context, tenantID uuid.UUID, acct dbq.WhatsappAccount, token string) (int, error)
}

func NewWorker(d *db.DB, keys *envelope.Keyring, meta Meta, templates TemplateSyncer, log *slog.Logger) *Worker {
	return &Worker{db: d, keys: keys, meta: meta, templates: templates, log: log}
}

// coexistenceError explains Meta's "not registered" reply to a coexistence sign-up: the number
// has to be in use on the WhatsApp Business app, which a new or virtual number is not.
func coexistenceError(err error) error {
	var me *metaclient.Error
	if errors.As(err, &me) && me.Code == 133010 {
		return &stepError{code: "meta_133010", msg: "This number is not active on the WhatsApp Business app, so there are no chats to bring over. " +
			"Use Start again and pick \"A new number\", or first set the number up in the WhatsApp Business app on a phone."}
	}
	return err
}

// stepError is a failure the client has to act on; it stops the job instead of retrying.
type stepError struct {
	code, msg string
}

func (e *stepError) Error() string { return e.code + ": " + e.msg }

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	a := job.Args
	ctx = metaclient.WithTenant(ctx, a.TenantID.String())
	log := w.log.With("session_id", a.SessionID, "tenant_id", a.TenantID, "attempt", job.Attempt)

	err := w.run(ctx, a, log)
	if err == nil {
		return nil
	}

	var se *stepError
	var me *metaclient.Error
	final := job.Attempt >= job.MaxAttempts
	switch {
	case errors.As(err, &se):
		w.fail(ctx, a, se.code, se.msg)
		return river.JobCancel(err)
	case errors.As(err, &me) && !me.Retryable():
		code, msg := describe(err)
		w.fail(ctx, a, code, msg)
		return river.JobCancel(err)
	case final:
		code, msg := describe(err)
		w.fail(ctx, a, code, msg+" We tried several times; use Retry to try again.")
	}
	log.Warn("onboarding step failed, will retry", "err", err)
	return err
}

func (w *Worker) run(ctx context.Context, a Args, log *slog.Logger) error {
	for {
		var (
			sess  dbq.OnboardingSession
			acct  dbq.WhatsappAccount
			token string
		)
		err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			var err error
			if sess, err = q.GetOnboardingSession(ctx, a.SessionID); err != nil {
				return err
			}
			if sess.WabaID == nil {
				return &stepError{"internal", "The session has no WhatsApp Business Account."}
			}
			if acct, err = q.GetWhatsAppAccountByWabaID(ctx, *sess.WabaID); err != nil {
				return err
			}
			token, err = credentials.Token(ctx, q, w.keys, acct)
			return err
		})
		if db.IsNotFound(err) {
			log.Warn("onboarding session or account gone; dropping job")
			return river.JobCancel(err)
		}
		if err != nil {
			return err
		}

		next, err := w.step(ctx, a.TenantID, sess, acct, token)
		if err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		log.Info("onboarding step done", "step", next)
		if next == dbq.OnboardingStepCompleted {
			return nil
		}
	}
}

// step performs the work after sess.Step and returns the step reached ("" when nothing is left).
func (w *Worker) step(ctx context.Context, tenantID uuid.UUID, sess dbq.OnboardingSession, acct dbq.WhatsappAccount, token string) (dbq.OnboardingStep, error) {
	switch sess.Step {
	case dbq.OnboardingStepTokenExchanged:
		if err := w.meta.SubscribeApp(ctx, token, acct.WabaID); err != nil {
			return "", err
		}
		waba, err := w.meta.GetWABA(ctx, token, acct.WabaID)
		if err != nil {
			return "", err
		}
		return dbq.OnboardingStepWebhooksSubscribed, w.commit(ctx, tenantID, sess.ID, dbq.OnboardingStepWebhooksSubscribed, func(q *dbq.Queries) error {
			if err := q.UpdateWhatsAppAccountDetails(ctx, dbq.UpdateWhatsAppAccountDetailsParams{
				ID: acct.ID, Name: nonEmpty(waba.Name), Currency: nonEmpty(waba.Currency), TimezoneID: nonEmpty(waba.TimezoneID),
			}); err != nil {
				return err
			}
			return q.MarkWebhooksSubscribed(ctx, acct.ID)
		})

	case dbq.OnboardingStepWebhooksSubscribed:
		pn, err := w.resolveNumber(ctx, token, sess)
		if err != nil {
			return "", err
		}
		phone, err := w.saveNumber(ctx, tenantID, sess, acct, pn)
		if err != nil {
			return "", err
		}
		if sess.Flow == dbq.OnboardingFlowCoexistence {
			// The number stays registered to the Business app; ask Meta to send its contacts.
			if err := w.meta.RequestSMBAppData(ctx, token, pn.ID, metaclient.SyncContacts); err != nil {
				return "", coexistenceError(err)
			}
			return dbq.OnboardingStepContactsSyncRequested, w.commit(ctx, tenantID, sess.ID, dbq.OnboardingStepContactsSyncRequested, nil)
		}
		// The PIN is saved before registering, so a retry after a lost response re-uses it
		// instead of trying to change the number's PIN.
		pinEnc := phone.TwoStepPinEnc
		if phone.RegisteredAt == nil {
			var pin string
			if pinEnc != nil {
				pt, err := w.keys.OpenCompact(pinEnc, pinAAD(tenantID, phone.ID))
				if err != nil {
					return "", err
				}
				pin = string(pt)
			} else {
				if pin, err = newPIN(); err != nil {
					return "", err
				}
				if pinEnc, err = w.keys.SealCompact([]byte(pin), pinAAD(tenantID, phone.ID)); err != nil {
					return "", err
				}
				err = w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
					return q.SetPhoneTwoStepPin(ctx, dbq.SetPhoneTwoStepPinParams{ID: phone.ID, TwoStepPinEnc: pinEnc})
				})
				if err != nil {
					return "", err
				}
			}
			if err := w.meta.RegisterPhoneNumber(ctx, token, pn.ID, pin); err != nil {
				return "", err
			}
		}
		return dbq.OnboardingStepNumberRegistered, w.commit(ctx, tenantID, sess.ID, dbq.OnboardingStepNumberRegistered, func(q *dbq.Queries) error {
			return q.MarkPhoneRegistered(ctx, dbq.MarkPhoneRegisteredParams{ID: phone.ID, TwoStepPinEnc: pinEnc})
		})

	case dbq.OnboardingStepContactsSyncRequested:
		if err := w.meta.RequestSMBAppData(ctx, token, deref(sess.PhoneNumberID), metaclient.SyncHistory); err != nil {
			return "", err
		}
		return dbq.OnboardingStepHistorySyncRequested, w.commit(ctx, tenantID, sess.ID, dbq.OnboardingStepHistorySyncRequested, nil)

	case dbq.OnboardingStepNumberRegistered, dbq.OnboardingStepHistorySyncRequested:
		pn, err := w.meta.GetPhoneNumber(ctx, token, deref(sess.PhoneNumberID))
		if err != nil {
			return "", err
		}
		if _, err := w.saveNumber(ctx, tenantID, sess, acct, pn); err != nil {
			return "", err
		}
		return dbq.OnboardingStepDetailsSynced, w.commit(ctx, tenantID, sess.ID, dbq.OnboardingStepDetailsSynced, nil)

	case dbq.OnboardingStepDetailsSynced:
		// Templates the business already has (from WhatsApp Manager or the Business app) become
		// usable right away. A failure does not block the connection; Sync on the Templates page retries.
		if n, err := w.templates.SyncAccount(ctx, tenantID, acct, token); err != nil {
			w.log.Warn("onboarding: template sync failed", "session_id", sess.ID, "err", err)
		} else {
			w.log.Info("onboarding: templates synced", "session_id", sess.ID, "count", n)
		}
		return dbq.OnboardingStepCompleted, w.commit(ctx, tenantID, sess.ID, dbq.OnboardingStepCompleted, func(q *dbq.Queries) error {
			phone, err := q.GetPhoneNumberByMetaID(ctx, deref(sess.PhoneNumberID))
			if err != nil {
				return err
			}
			if err := q.SetPhoneNumberStatus(ctx, dbq.SetPhoneNumberStatusParams{ID: phone.ID, Status: dbq.ConnectionStatusConnected}); err != nil {
				return err
			}
			return q.MarkWhatsAppAccountConnected(ctx, acct.ID)
		})

	case dbq.OnboardingStepCompleted, dbq.OnboardingStepCancelled, dbq.OnboardingStepFailed:
		return "", nil
	}
	return "", &stepError{"internal", fmt.Sprintf("Onboarding cannot continue from step %q.", sess.Step)}
}

// resolveNumber returns the number being connected. Coexistence sign-ups can report only the
// WABA, in which case the WABA's numbers are listed (a coexistence WABA holds exactly one).
func (w *Worker) resolveNumber(ctx context.Context, token string, sess dbq.OnboardingSession) (*metaclient.PhoneNumber, error) {
	if sess.PhoneNumberID != nil {
		return w.meta.GetPhoneNumber(ctx, token, *sess.PhoneNumberID)
	}
	nums, err := w.meta.ListPhoneNumbers(ctx, token, deref(sess.WabaID))
	if err != nil {
		return nil, err
	}
	if len(nums) == 0 {
		return nil, &stepError{"no_phone_number", "Meta shows no phone number on this WhatsApp Business Account. Add a number in the Meta popup and connect again."}
	}
	return &nums[0], nil
}

// saveNumber upserts the phone_numbers row and pins the number ID on the session.
func (w *Worker) saveNumber(ctx context.Context, tenantID uuid.UUID, sess dbq.OnboardingSession, acct dbq.WhatsappAccount, pn *metaclient.PhoneNumber) (dbq.PhoneNumber, error) {
	var phone dbq.PhoneNumber
	err := w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		owner, err := q.PhoneNumberOwner(ctx, pn.ID)
		if err != nil {
			return err
		}
		if owner != uuid.Nil && owner != tenantID {
			return &stepError{"already_connected", "This number is already connected to another Ecogo workspace."}
		}
		if err := billing.NumberRoom(ctx, q, pn.ID); err != nil {
			var he *httpx.Error
			if errors.As(err, &he) {
				return &stepError{he.Code, he.Message}
			}
			return err
		}
		if sess.PhoneNumberID == nil {
			if err := q.SetOnboardingPhoneNumberID(ctx, dbq.SetOnboardingPhoneNumberIDParams{ID: sess.ID, PhoneNumberID: &pn.ID}); err != nil {
				return err
			}
		}
		display := pn.DisplayPhoneNumber
		if display == "" {
			display = pn.ID
		}
		phone, err = q.UpsertPhoneNumber(ctx, dbq.UpsertPhoneNumberParams{
			ID: db.NewID(), TenantID: tenantID, WhatsappAccountID: acct.ID, PhoneNumberID: pn.ID,
			DisplayPhoneNumber: display, VerifiedName: nonEmpty(pn.VerifiedName), NameStatus: nonEmpty(pn.NameStatus),
			QualityRating: quality(pn.QualityRating), MessagingLimitTier: nonEmpty(pn.MessagingLimitTier),
			CodeVerificationStatus: nonEmpty(pn.CodeVerificationStatus),
			IsCoexistence:          sess.Flow == dbq.OnboardingFlowCoexistence || pn.IsOnBizApp,
		})
		return err
	})
	return phone, err
}

func (w *Worker) commit(ctx context.Context, tenantID, sessionID uuid.UUID, step dbq.OnboardingStep, fn func(q *dbq.Queries) error) error {
	return w.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if fn != nil {
			if err := fn(q); err != nil {
				return err
			}
		}
		return q.SetOnboardingStep(ctx, dbq.SetOnboardingStepParams{ID: sessionID, Step: step})
	})
}

func (w *Worker) fail(ctx context.Context, a Args, code, msg string) {
	ctx = context.WithoutCancel(ctx)
	err := w.db.InTenant(ctx, a.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		sess, err := q.GetOnboardingSession(ctx, a.SessionID)
		if err != nil {
			return err
		}
		if sess.WabaID != nil {
			if acct, err := q.GetWhatsAppAccountByWabaID(ctx, *sess.WabaID); err == nil {
				if err := q.MarkWhatsAppAccountError(ctx, acct.ID); err != nil {
					return err
				}
			}
		}
		return q.FailOnboardingSession(ctx, dbq.FailOnboardingSessionParams{ID: a.SessionID, ErrorCode: &code, ErrorMessage: &msg})
	})
	if err != nil {
		w.log.Error("onboarding: record failure", "session_id", a.SessionID, "err", err)
	}
}

func newPIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func pinAAD(tenantID, phoneID uuid.UUID) []byte {
	return []byte("phone_numbers.two_step_pin:" + tenantID.String() + ":" + phoneID.String())
}

func quality(s string) dbq.QualityRating {
	q := dbq.QualityRating(strings.ToLower(s))
	if q.Valid() {
		return q
	}
	return dbq.QualityRatingUnknown
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
