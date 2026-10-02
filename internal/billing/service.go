// Package billing is D9's own billing: our monthly plans, paid through Razorpay subscriptions.
// Every workspace starts on a 14-day trial (a database trigger creates it with the tenant).
// Owners pick a plan on the Billing tab and pay on Razorpay's page; Razorpay's webhooks then
// move the subscription between trialing, active, past_due and cancelled. Sending messages
// and starting campaigns stop when the trial or a cancelled plan has ended, or 7 days after a
// failed charge.
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/razorpay"
)

// Grace is how long sending keeps working after a failed charge, while Razorpay retries.
const Grace = 7 * 24 * time.Hour

// cycles is how many monthly charges a Razorpay subscription may make (Razorpay requires a
// limit); ten years.
const cycles = 120

type Service struct {
	db            *db.DB
	rp            *razorpay.Client
	webhookSecret string
	seller        config.Seller
	jobs          jobs.Inserter
	log           *slog.Logger
	now           func() time.Time
}

func NewService(d *db.DB, rp *razorpay.Client, webhookSecret string, seller config.Seller, inserter jobs.Inserter, log *slog.Logger) *Service {
	return &Service{db: d, rp: rp, webhookSecret: webhookSecret, seller: seller, jobs: inserter, log: log, now: time.Now}
}

// InternalRoutes mounts /internal/billing for the dashboard. Only owners manage billing.
func (s *Service) InternalRoutes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner))
	r.Get("/", httpx.Handler(s.log, s.get))
	r.Post("/subscribe", httpx.Handler(s.log, s.subscribe))
	r.Post("/cancel", httpx.Handler(s.log, s.cancel))
	r.Post("/seats", httpx.Handler(s.log, s.seats))
	r.Get("/profile", httpx.Handler(s.log, s.getProfile))
	r.Put("/profile", httpx.Handler(s.log, s.saveProfile))
	r.Get("/invoices", httpx.Handler(s.log, s.listInvoices))
	r.Get("/invoices/{id}/view", httpx.Handler(s.log, s.view))
}

// Usable reports whether a workspace on this subscription may send.
func Usable(sub dbq.Subscription, now time.Time) bool {
	switch sub.Status {
	case dbq.SubscriptionStatusActive:
		return true
	case dbq.SubscriptionStatusPastDue:
		return now.Before(sub.CurrentPeriodEnd.Add(Grace))
	default: // trialing, cancelled: until the period ends
		return now.Before(sub.CurrentPeriodEnd)
	}
}

var ErrPaymentRequired = httpx.NewError(http.StatusPaymentRequired, "payment_required",
	"Your Ecogo plan has ended. The workspace owner can choose a plan under Settings, Billing.")

// Check refuses new sends when the workspace's plan has ended. Run it inside the tenant.
func Check(ctx context.Context, q *dbq.Queries, now time.Time) error {
	sub, err := q.GetSubscription(ctx)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !Usable(sub, now) {
		return ErrPaymentRequired
	}
	return nil
}

// ErrNumberLimit is returned when a workspace has used all the WhatsApp numbers its plan includes.
func ErrNumberLimit(limit int32) error {
	return httpx.NewError(http.StatusConflict, "plan_limit",
		fmt.Sprintf("Your plan includes %d WhatsApp number%s and all are in use. Disconnect a number or choose a larger plan under Settings, Billing.",
			limit, plural(limit)))
}

func plural(n int32) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// NumberRoom refuses a new WhatsApp number when the plan's numbers are all in use. exceptID
// is the Meta ID of a number being (re)connected, which does not count twice. A trial with no
// plan chosen has no limit. Run it inside the tenant.
func NumberRoom(ctx context.Context, q *dbq.Queries, exceptID string) error {
	plan, err := q.CurrentPlan(ctx)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	used, err := q.NumbersInUse(ctx, exceptID)
	if err != nil {
		return err
	}
	if used >= plan.IncludedNumbers {
		return ErrNumberLimit(plan.IncludedNumbers)
	}
	return nil
}

// Plan is a plan as customers and admins see it.
type Plan struct {
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	PriceMinor      int64   `json:"price_minor"`
	Currency        string  `json:"currency"`
	IncludedNumbers int32   `json:"included_numbers"`
	IncludedSeats   int32   `json:"included_seats"`
	ExtraSeatMinor  int64   `json:"extra_seat_minor"`
	RazorpayPlanID  *string `json:"razorpay_plan_id,omitempty"`
	// ExtraSeatRazorpayPlanID is the Razorpay plan that charges one extra seat a month (admins only).
	ExtraSeatRazorpayPlanID *string `json:"extra_seat_razorpay_plan_id,omitempty"`
	// ExtraSeatsAvailable tells customers they can buy seats beyond the included ones.
	ExtraSeatsAvailable bool  `json:"extra_seats_available"`
	SortOrder           int32 `json:"sort_order"`
	Active              bool  `json:"is_active"`
	// AIRepliesPerMonth is how many chatbot AI answers the plan includes each billing month; 0 for none.
	AIRepliesPerMonth int32 `json:"ai_replies_per_month"`
}

func PlanView(p dbq.Plan) Plan {
	return Plan{Code: p.Code, Name: p.Name, PriceMinor: p.PriceMinor, Currency: p.Currency,
		IncludedNumbers: p.IncludedNumbers, IncludedSeats: p.IncludedSeats, ExtraSeatMinor: p.ExtraSeatMinor,
		RazorpayPlanID: p.RazorpayPlanID, ExtraSeatRazorpayPlanID: p.ExtraSeatRazorpayPlanID,
		ExtraSeatsAvailable: p.RazorpayPlanID != nil && p.ExtraSeatRazorpayPlanID != nil, SortOrder: p.SortOrder, Active: p.IsActive, AIRepliesPerMonth: p.AiRepliesPerMonth}
}

// Subscription is the workspace's billing state.
type Subscription struct {
	Status            string    `json:"status"`
	PlanCode          *string   `json:"plan_code"`
	PeriodStart       time.Time `json:"current_period_start"`
	PeriodEnd         time.Time `json:"current_period_end"`
	CancelAtPeriodEnd bool      `json:"cancel_at_period_end"`
	Usable            bool      `json:"usable"`
	// PaymentPending is set after the owner was sent to Razorpay and before it confirmed.
	PaymentPending bool `json:"payment_pending"`
	// ExtraSeats is how many seats beyond the plan's included ones are paid for.
	ExtraSeats int32 `json:"extra_seats"`
}

func SubscriptionView(sub dbq.Subscription, now time.Time) Subscription {
	return Subscription{Status: string(sub.Status), PlanCode: sub.PlanCode, PeriodStart: sub.CurrentPeriodStart,
		PeriodEnd: sub.CurrentPeriodEnd, CancelAtPeriodEnd: sub.CancelAtPeriodEnd, Usable: Usable(sub, now),
		PaymentPending: sub.ProviderSubscriptionID != nil && sub.PlanCode == nil, ExtraSeats: sub.ExtraSeats}
}

type Overview struct {
	Subscription    Subscription `json:"subscription"`
	Plan            *Plan        `json:"plan"`
	Plans           []Plan       `json:"plans"`
	ConnectedNumber int32        `json:"connected_numbers"`
	Seats           int32        `json:"seats"`
	PaymentsEnabled bool         `json:"payments_enabled"`
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := Overview{Plans: []Plan{}, PaymentsEnabled: s.rp.Configured()}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		sub, err := q.GetSubscription(r.Context())
		if err != nil {
			return err
		}
		out.Subscription = SubscriptionView(sub, s.now())
		plans, err := q.ListPlans(r.Context(), false)
		if err != nil {
			return err
		}
		for _, pl := range plans {
			if sub.PlanCode != nil && pl.Code == *sub.PlanCode {
				v := PlanView(pl)
				out.Plan = &v
			}
			// Only plans Razorpay can charge are offered.
			if pl.IsActive && pl.RazorpayPlanID != nil {
				v := PlanView(pl)
				v.RazorpayPlanID, v.ExtraSeatRazorpayPlanID = nil, nil
				out.Plans = append(out.Plans, v)
			}
		}
		u, err := q.BillingUsage(r.Context())
		out.ConnectedNumber, out.Seats = u.ConnectedNumbers, u.Seats
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

var errPaymentsOff = httpx.NewError(http.StatusServiceUnavailable, "payments_unavailable",
	"Online payment is not set up yet. Contact Ecogo support to subscribe.")

// subscribe starts paying for a plan. A new subscription returns Razorpay's payment page to
// open; an active one switches plan from its next cycle.
func (s *Service) subscribe(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Plan string `json:"plan"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if !s.rp.Configured() {
		return errPaymentsOff
	}
	ctx := r.Context()
	var (
		plan   dbq.Plan
		sub    dbq.Subscription
		tenant dbq.Tenant
		user   dbq.User
	)
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		plan, err = q.GetPlan(ctx, req.Plan)
		if db.IsNotFound(err) || (err == nil && (!plan.IsActive || plan.RazorpayPlanID == nil)) {
			return httpx.BadRequest("plan", "Choose one of the plans shown.")
		}
		if err != nil {
			return err
		}
		if sub, err = q.GetSubscription(ctx); err != nil {
			return err
		}
		// A plan that includes fewer numbers than are connected would leave the workspace over its limit.
		if used, err := q.NumbersInUse(ctx, ""); err != nil {
			return err
		} else if used > plan.IncludedNumbers {
			return httpx.NewError(http.StatusConflict, "plan_limit", fmt.Sprintf(
				"You have %d WhatsApp numbers connected and %s includes %d. Disconnect numbers or choose a larger plan.",
				used, plan.Name, plan.IncludedNumbers))
		}
		if u, err := q.BillingUsage(ctx); err != nil {
			return err
		} else if u.Seats > plan.IncludedSeats+sub.ExtraSeats {
			return httpx.NewError(http.StatusConflict, "seat_limit", fmt.Sprintf(
				"You have %d members and %s includes %d seats. Remove members or choose a larger plan.",
				u.Seats, plan.Name, plan.IncludedSeats+sub.ExtraSeats))
		}
		if tenant, err = q.GetTenant(ctx, p.TenantID); err != nil {
			return err
		}
		user, err = q.GetUserByID(ctx, p.UserID)
		return err
	})
	if err != nil {
		return err
	}

	now := s.now()
	if sub.Status == dbq.SubscriptionStatusActive && sub.ProviderSubscriptionID != nil {
		if sub.CancelAtPeriodEnd {
			return httpx.NewError(http.StatusConflict, "conflict",
				"Your plan is set to end on "+sub.CurrentPeriodEnd.Format("2 Jan 2006")+". Choose a plan again after that date.")
		}
		if sub.PlanCode != nil && *sub.PlanCode == plan.Code {
			return httpx.NewError(http.StatusConflict, "conflict", "You are already on this plan.")
		}
		if _, err := s.rp.ChangePlan(ctx, *sub.ProviderSubscriptionID, *plan.RazorpayPlanID); err != nil {
			return s.providerError(err)
		}
		if err := s.audit(r, "subscription.change_plan", plan.Code); err != nil {
			return err
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"scheduled": true})
		return nil
	}

	// An earlier checkout (unpaid, or chosen during the trial) is cancelled first, so its
	// payment link cannot charge the customer alongside the new one.
	if sub.ProviderSubscriptionID != nil && sub.Status != dbq.SubscriptionStatusCancelled {
		if _, err := s.rp.Cancel(ctx, *sub.ProviderSubscriptionID, false); err != nil {
			var re *razorpay.Error
			if !errors.As(err, &re) || re.HTTPStatus != http.StatusBadRequest {
				return s.providerError(err)
			}
			// 400: Razorpay has already ended it.
			s.log.Info("earlier razorpay subscription not cancelled", "subscription", *sub.ProviderSubscriptionID, "description", re.Description)
		}
	}

	notes := map[string]string{"tenant_id": p.TenantID.String()}
	cust := ""
	if sub.ProviderCustomerID != nil {
		cust = *sub.ProviderCustomerID
	} else {
		c, err := s.rp.CreateCustomer(ctx, tenant.Name, user.Email, notes)
		if err != nil {
			return s.providerError(err)
		}
		cust = c.ID
	}
	ns := razorpay.NewSubscription{PlanID: *plan.RazorpayPlanID, CustomerID: cust, TotalCount: cycles, Notes: notes}
	// The rest of a trial stays free: the first charge waits for its end.
	if sub.Status == dbq.SubscriptionStatusTrialing && sub.CurrentPeriodEnd.After(now.Add(time.Hour)) {
		ns.StartAt = &sub.CurrentPeriodEnd
	}
	rs, err := s.rp.CreateSubscription(ctx, ns)
	if err != nil {
		return s.providerError(err)
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.SetProviderSubscription(ctx, dbq.SetProviderSubscriptionParams{
			ProviderCustomerID: &cust, ProviderSubscriptionID: &rs.ID, TenantID: p.TenantID})
	})
	if err != nil {
		return err
	}
	if err := s.audit(r, "subscription.checkout", plan.Code); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"payment_url": rs.ShortURL})
	return nil
}

func (s *Service) cancel(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	var sub dbq.Subscription
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		sub, err = q.GetSubscription(ctx)
		return err
	})
	if err != nil {
		return err
	}
	if sub.Status == dbq.SubscriptionStatusCancelled || sub.ProviderSubscriptionID == nil || sub.PlanCode == nil {
		return httpx.NewError(http.StatusConflict, "conflict", "There is no paid plan to cancel.")
	}
	if !s.rp.Configured() {
		return errPaymentsOff
	}
	// A paid cycle runs to its end; a plan chosen during the trial stops before its first charge.
	if _, err := s.rp.Cancel(ctx, *sub.ProviderSubscriptionID, sub.Status != dbq.SubscriptionStatusTrialing); err != nil {
		return s.providerError(err)
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if sub.Status == dbq.SubscriptionStatusTrialing {
			err = q.DropTrialPlan(ctx, p.TenantID)
		} else {
			err = q.SetCancelAtPeriodEnd(ctx, dbq.SetCancelAtPeriodEndParams{Cancel: true, TenantID: p.TenantID})
		}
		if err != nil {
			return err
		}
		sub, err = q.GetSubscription(ctx)
		return err
	})
	if err != nil {
		return err
	}
	if err := s.audit(r, "subscription.cancel", ""); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, SubscriptionView(sub, s.now()))
	return nil
}

func (s *Service) audit(r *http.Request, action, plan string) error {
	p, _ := auth.PrincipalFrom(r.Context())
	return s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		target := "subscription"
		meta, _ := json.Marshal(map[string]string{"plan": plan})
		return q.InsertAuditLog(r.Context(), dbq.InsertAuditLogParams{
			TenantID: &p.TenantID, ActorType: dbq.ActorTypeUser, ActorID: &p.UserID, Action: action,
			TargetType: &target, TargetID: ptr(p.TenantID.String()), Ip: auth.ClientIP(r), Metadata: meta,
		})
	})
}

func ptr[T any](v T) *T { return &v }

func (s *Service) providerError(err error) error {
	var re *razorpay.Error
	if errors.As(err, &re) {
		s.log.Warn("razorpay error", "status", re.HTTPStatus, "code", re.Code, "description", re.Description)
		return httpx.NewError(http.StatusBadGateway, "payment_provider_error",
			"Razorpay could not set up the payment: "+re.Description)
	}
	return err
}

// Webhook receives Razorpay's subscription events at /webhooks/razorpay.
func (s *Service) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || !razorpay.VerifySignature(body, r.Header.Get("X-Razorpay-Signature"), s.webhookSecret) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var ev razorpay.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if err := s.apply(r.Context(), r.Header.Get("X-Razorpay-Event-Id"), ev); err != nil {
		s.log.Error("razorpay webhook", "event", ev.Event, "err", err)
		http.Error(w, "retry", http.StatusInternalServerError) // Razorpay retries
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Service) apply(ctx context.Context, eventID string, ev razorpay.Event) error {
	if ev.Payload.Subscription == nil {
		return nil
	}
	rs := ev.Payload.Subscription.Entity
	var tenantID uuid.UUID
	err := s.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		tenantID, err = q.SubscriptionTenant(ctx, rs.ID)
		return err
	})
	if db.IsNotFound(err) {
		s.log.Info("razorpay event for unknown subscription", "event", ev.Event, "subscription", rs.ID)
		return nil
	}
	if err != nil {
		return err
	}
	return s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, tx pgx.Tx) error {
		if eventID != "" {
			n, err := q.RecordRazorpayEvent(ctx, dbq.RecordRazorpayEventParams{ID: eventID, Event: ev.Event})
			if err != nil || n == 0 {
				return err
			}
		}
		sub, err := q.GetSubscriptionForUpdate(ctx)
		if err != nil {
			return err
		}
		if sub.SeatProviderSubscriptionID != nil && *sub.SeatProviderSubscriptionID == rs.ID {
			extra, ended, ok := seatEvent(ev.Event, rs)
			if !ok {
				return nil
			}
			if err := q.SetExtraSeats(ctx, dbq.SetExtraSeatsParams{TenantID: tenantID, ExtraSeats: extra, Ended: ended}); err != nil {
				return err
			}
			if ev.Event == "subscription.charged" {
				desc := fmt.Sprintf("Ecogo WhatsApp: %d extra team seat%s", extra, plural(extra))
				if err := s.issueInvoice(ctx, q, tx, tenantID, payment(ev), desc, unix(rs.CurrentStart), unix(rs.CurrentEnd)); err != nil {
					return err
				}
			}
			target, meta := "subscription", json.RawMessage(`{"event":"`+ev.Event+`","seats":true}`)
			return q.InsertAuditLog(ctx, dbq.InsertAuditLogParams{
				TenantID: &tenantID, ActorType: dbq.ActorTypeSystem, Action: "subscription.seats",
				TargetType: &target, TargetID: &rs.ID, Metadata: meta,
			})
		}
		next := Transition(sub, ev.Event, rs)
		if next == nil {
			return nil
		}
		planName := "Ecogo WhatsApp plan"
		if rs.PlanID != "" {
			pl, err := q.GetPlanByRazorpayID(ctx, &rs.PlanID)
			if err == nil {
				next.PlanCode = &pl.Code
				planName = pl.Name + " plan"
			} else if !db.IsNotFound(err) {
				return err
			}
		}
		next.TenantID = tenantID
		applied, err := q.ApplySubscriptionState(ctx, *next)
		if err != nil {
			return err
		}
		if ev.Event == "subscription.charged" {
			desc := "Ecogo WhatsApp: " + planName
			if err := s.issueInvoice(ctx, q, tx, tenantID, payment(ev), desc, &applied.CurrentPeriodStart, &applied.CurrentPeriodEnd); err != nil {
				return err
			}
		}
		target, meta := "subscription", json.RawMessage(`{"event":"`+ev.Event+`"}`)
		return q.InsertAuditLog(ctx, dbq.InsertAuditLogParams{
			TenantID: &tenantID, ActorType: dbq.ActorTypeSystem, Action: "subscription." + string(next.Status),
			TargetType: &target, TargetID: &rs.ID, Metadata: meta,
		})
	})
}

// payment is the charge a subscription.charged event reports, if it carries one.
func payment(ev razorpay.Event) *razorpay.Payment {
	if ev.Payload.Payment == nil {
		return nil
	}
	return &ev.Payload.Payment.Entity
}

func unix(v *int64) *time.Time {
	if v == nil || *v == 0 {
		return nil
	}
	t := time.Unix(*v, 0).UTC()
	return &t
}

// Transition maps a Razorpay subscription event onto our subscription; nil means no change.
func Transition(sub dbq.Subscription, event string, rs razorpay.Subscription) *dbq.ApplySubscriptionStateParams {
	next := &dbq.ApplySubscriptionStateParams{Status: sub.Status, CancelAtPeriodEnd: sub.CancelAtPeriodEnd}
	switch event {
	case "subscription.authenticated":
		// The mandate is set up; a trial keeps running until the first charge.
		if sub.Status != dbq.SubscriptionStatusTrialing {
			next.Status = dbq.SubscriptionStatusActive
		}
	case "subscription.activated", "subscription.charged", "subscription.resumed":
		next.Status = dbq.SubscriptionStatusActive
		next.PeriodStart, next.PeriodEnd = unix(rs.CurrentStart), unix(rs.CurrentEnd)
	case "subscription.pending", "subscription.halted":
		next.Status = dbq.SubscriptionStatusPastDue
	case "subscription.cancelled", "subscription.completed", "subscription.expired":
		next.CancelAtPeriodEnd = false
		if sub.Status == dbq.SubscriptionStatusTrialing {
			// Cancelled before the first charge: the trial runs out as it would have.
			return next
		}
		next.Status = dbq.SubscriptionStatusCancelled
		if end := unix(rs.EndedAt); end != nil && end.Before(sub.CurrentPeriodEnd) {
			next.PeriodEnd = end
		}
	case "subscription.updated":
		// A plan change; the plan is read from the payload.
	default:
		return nil
	}
	return next
}

// TrialAIReplies is how many AI answers a workspace on a free trial (no plan chosen) can use.
const TrialAIReplies = 50

// AIAllowance is a workspace's AI replies for the current billing period.
type AIAllowance struct {
	Limit       int32     `json:"limit"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

// AIAllowanceFor returns the AI replies the workspace's plan includes and the period they count
// over. Run it inside the tenant.
func AIAllowanceFor(ctx context.Context, q *dbq.Queries) (AIAllowance, error) {
	sub, err := q.GetSubscription(ctx)
	if db.IsNotFound(err) {
		return AIAllowance{}, nil
	}
	if err != nil {
		return AIAllowance{}, err
	}
	a := AIAllowance{Limit: TrialAIReplies, PeriodStart: sub.CurrentPeriodStart, PeriodEnd: sub.CurrentPeriodEnd}
	if sub.PlanCode != nil {
		plan, err := q.GetPlan(ctx, *sub.PlanCode)
		if err != nil {
			return AIAllowance{}, err
		}
		a.Limit = plan.AiRepliesPerMonth
	}
	return a, nil
}
