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
	"strings"
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
// limit); ten years. yearlyCycles is the same for a yearly plan.
const (
	cycles       = 120
	yearlyCycles = 10
)

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
		// A plan granted from the admin console with an end date stops at that date; Razorpay
		// says when a paid one ends.
		if sub.AdminGranted && sub.CancelAtPeriodEnd {
			return now.Before(sub.CurrentPeriodEnd)
		}
		return true
	case dbq.SubscriptionStatusPastDue:
		return now.Before(sub.CurrentPeriodEnd.Add(Grace))
	default: // trialing, cancelled: until the period ends
		return now.Before(sub.CurrentPeriodEnd)
	}
}

var ErrPaymentRequired = httpx.NewError(http.StatusPaymentRequired, "payment_required",
	"Your Ecogo plan has ended. The workspace owner can choose a plan under Settings, Billing.")

// Check refuses new sends when the workspace's plan has ended or its messages for the month
// are used up. Run it inside the tenant.
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
	room, err := MessageRoom(ctx, q, now)
	if err != nil {
		return err
	}
	if room == 0 {
		return ErrMessageLimit(ctx, q, now)
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
	// ContactsLimit and MessagesPerMonth are the plan's limits; null for none.
	ContactsLimit    *int32 `json:"contacts_limit"`
	MessagesPerMonth *int32 `json:"messages_per_month"`
	// YearlyPriceMinor is the price for a year paid at once; null when the plan is monthly only.
	YearlyPriceMinor *int64 `json:"yearly_price_minor"`
	// YearlyRazorpayPlanID is the Razorpay plan that charges the yearly price (admins only).
	YearlyRazorpayPlanID *string `json:"yearly_razorpay_plan_id,omitempty"`
	// YearlyAvailable tells customers the plan can be paid yearly.
	YearlyAvailable bool `json:"yearly_available"`
	// Free plans start without a payment.
	Free bool `json:"free"`
	// Tagline and Highlights are the plan card's text; Popular marks the plan to recommend.
	Tagline    string   `json:"tagline"`
	Highlights []string `json:"highlights"`
	Popular    bool     `json:"is_popular"`
}

func PlanView(p dbq.Plan) Plan {
	return Plan{Code: p.Code, Name: p.Name, PriceMinor: p.PriceMinor, Currency: p.Currency,
		IncludedNumbers: p.IncludedNumbers, IncludedSeats: p.IncludedSeats, ExtraSeatMinor: p.ExtraSeatMinor,
		RazorpayPlanID: p.RazorpayPlanID, ExtraSeatRazorpayPlanID: p.ExtraSeatRazorpayPlanID,
		ExtraSeatsAvailable: p.RazorpayPlanID != nil && p.ExtraSeatRazorpayPlanID != nil, SortOrder: p.SortOrder, Active: p.IsActive, AIRepliesPerMonth: p.AiRepliesPerMonth,
		ContactsLimit: p.ContactsLimit, MessagesPerMonth: p.MessagesPerMonth, YearlyPriceMinor: p.YearlyPriceMinor,
		YearlyRazorpayPlanID: p.YearlyRazorpayPlanID, YearlyAvailable: p.YearlyPriceMinor != nil && p.YearlyRazorpayPlanID != nil,
		Free: IsFree(p), Tagline: p.Tagline, Highlights: append([]string{}, p.Highlights...), Popular: p.IsPopular}
}

// Offered reports whether customers can choose the plan: an active plan that is free or that
// Razorpay can charge.
func Offered(p dbq.Plan) bool {
	return p.IsActive && (IsFree(p) || p.RazorpayPlanID != nil)
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
	// AdminGranted is set when Ecogo staff put the workspace on its plan without payment.
	AdminGranted bool `json:"admin_granted"`
	// ExtraSeats is how many seats beyond the plan's included ones are paid for.
	ExtraSeats int32 `json:"extra_seats"`
	// Interval is month or year: which of the plan's prices is paid.
	Interval string `json:"billing_interval"`
}

func SubscriptionView(sub dbq.Subscription, now time.Time) Subscription {
	return Subscription{Status: string(sub.Status), PlanCode: sub.PlanCode, PeriodStart: sub.CurrentPeriodStart,
		PeriodEnd: sub.CurrentPeriodEnd, CancelAtPeriodEnd: sub.CancelAtPeriodEnd, Usable: Usable(sub, now),
		PaymentPending: (sub.ProviderSubscriptionID != nil && sub.PlanCode == nil) || sub.PendingProviderSubscriptionID != nil, ExtraSeats: sub.ExtraSeats,
		Interval: sub.BillingInterval, AdminGranted: sub.AdminGranted}
}

type Overview struct {
	Subscription    Subscription `json:"subscription"`
	Plan            *Plan        `json:"plan"`
	Plans           []Plan       `json:"plans"`
	ConnectedNumber int32        `json:"connected_numbers"`
	Seats           int32        `json:"seats"`
	Contacts        int32        `json:"contacts"`
	// MessagesThisMonth counts towards the plan's monthly messages, from MonthStart to MonthEnd.
	MessagesThisMonth int32     `json:"messages_this_month"`
	MonthStart        time.Time `json:"month_start"`
	MonthEnd          time.Time `json:"month_end"`
	PaymentsEnabled   bool      `json:"payments_enabled"`
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := Overview{Plans: []Plan{}, PaymentsEnabled: s.rp.Configured()}
	s.sync(r.Context(), p.TenantID)
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
				// A checkout started from the free plan waits for Razorpay like one from a trial.
				if (v.Free || sub.AdminGranted) && sub.ProviderSubscriptionID != nil {
					out.Subscription.PaymentPending = true
				}
			}
			// Only free plans and plans Razorpay can charge are offered.
			if Offered(pl) {
				v := PlanView(pl)
				v.RazorpayPlanID, v.ExtraSeatRazorpayPlanID, v.YearlyRazorpayPlanID = nil, nil, nil
				out.Plans = append(out.Plans, v)
			}
		}
		u, err := q.BillingUsage(r.Context())
		if err != nil {
			return err
		}
		out.ConnectedNumber, out.Seats = u.ConnectedNumbers, u.Seats
		if out.Contacts, err = q.ContactsInUse(r.Context()); err != nil {
			return err
		}
		out.MonthStart, out.MonthEnd = UsageMonth(sub, s.now())
		out.MessagesThisMonth, err = q.OutboundMessagesSince(r.Context(), out.MonthStart)
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
// open. A paid plan changes in place: a dearer plan billed the same way starts at once, any
// other change from the next cycle.
func (s *Service) subscribe(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Plan string `json:"plan"`
		// Interval is month (the default) or year.
		Interval string `json:"interval"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	switch req.Interval {
	case "":
		req.Interval = "month"
	case "month", "year":
	default:
		return httpx.BadRequest("interval", "interval must be month or year.")
	}
	ctx := r.Context()
	var (
		plan    dbq.Plan
		current *dbq.Plan
		sub     dbq.Subscription
		tenant  dbq.Tenant
		user    dbq.User
	)
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		plan, err = q.GetPlan(ctx, req.Plan)
		if db.IsNotFound(err) || (err == nil && !Offered(plan)) {
			return httpx.BadRequest("plan", "Choose one of the plans shown.")
		}
		if err != nil {
			return err
		}
		if req.Interval == "year" && !IsFree(plan) && (plan.YearlyPriceMinor == nil || plan.YearlyRazorpayPlanID == nil) {
			return httpx.BadRequest("interval", plan.Name+" is not sold yearly yet. Choose monthly billing.")
		}
		if sub, err = q.GetSubscription(ctx); err != nil {
			return err
		}
		if sub.PlanCode != nil {
			cur, err := q.GetPlan(ctx, *sub.PlanCode)
			if err != nil {
				return err
			}
			current = &cur
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
	if IsFree(plan) {
		return s.startFree(w, r, plan, sub, now)
	}
	if !s.rp.Configured() {
		return errPaymentsOff
	}
	rpPlan, total := *plan.RazorpayPlanID, cycles
	if req.Interval == "year" {
		rpPlan, total = *plan.YearlyRazorpayPlanID, yearlyCycles
	}

	// A plan the workspace pays Razorpay for changes in place. From the free plan or a granted
	// one, any earlier checkout is replaced by a new one below.
	unpaid := current != nil && (IsFree(*current) || sub.AdminGranted)
	if sub.Status == dbq.SubscriptionStatusActive && sub.ProviderSubscriptionID != nil && !unpaid {
		if sub.CancelAtPeriodEnd {
			return httpx.NewError(http.StatusConflict, "conflict",
				"Your plan is set to end on "+sub.CurrentPeriodEnd.Format("2 Jan 2006")+". Choose a plan again after that date.")
		}
		if sub.PlanCode != nil && *sub.PlanCode == plan.Code && sub.BillingInterval == req.Interval {
			return httpx.NewError(http.StatusConflict, "conflict", "You are already on this plan.")
		}
		// An upgrade (a dearer plan, billed the same way) starts now; Razorpay charges for it at
		// once. Anything else, a cheaper plan or a move between monthly and yearly, waits for
		// the end of the paid cycle.
		upgrade := current != nil && sub.BillingInterval == req.Interval &&
			MonthlyEquivalent(plan, req.Interval) > MonthlyEquivalent(*current, sub.BillingInterval)
		// An earlier replacement checkout not yet paid must not charge alongside this change.
		if err := s.dropPending(ctx, p.TenantID, sub); err != nil {
			return err
		}
		if _, err := s.rp.ChangePlan(ctx, *sub.ProviderSubscriptionID, rpPlan, upgrade); err != nil {
			if !inPlaceRefused(err) {
				return s.providerError(err)
			}
			return s.replaceSubscription(w, r, sub, plan, rpPlan, req.Interval, upgrade, now)
		}
		action := "subscription.change_plan"
		if upgrade {
			action = "subscription.upgrade"
		}
		if err := s.audit(r, action, plan.Code); err != nil {
			return err
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"scheduled": !upgrade, "upgraded": upgrade})
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
	ns := razorpay.NewSubscription{PlanID: rpPlan, CustomerID: cust, TotalCount: total, Notes: notes}
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
	httpx.JSON(w, http.StatusOK, s.checkout(rs))
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
	if free, err := s.onFreePlan(ctx, p.TenantID, sub); err != nil {
		return err
	} else if free {
		// A checkout started from the free plan: stop it; the free plan carries on.
		if _, err := s.rp.Cancel(ctx, *sub.ProviderSubscriptionID, false); err != nil {
			return s.providerError(err)
		}
		err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			if err := q.DropProviderSubscription(ctx, dbq.DropProviderSubscriptionParams{
				TenantID: p.TenantID, ProviderSubscriptionID: sub.ProviderSubscriptionID}); err != nil {
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
	if err := s.dropPending(ctx, p.TenantID, sub); err != nil {
		return err
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

// onFreePlan reports whether the workspace's current plan is one it does not pay Razorpay for:
// a free plan, or a plan granted from the admin console.
func (s *Service) onFreePlan(ctx context.Context, tenantID uuid.UUID, sub dbq.Subscription) (bool, error) {
	if sub.PlanCode == nil {
		return false, nil
	}
	if sub.AdminGranted {
		return true, nil
	}
	free := false
	err := s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		pl, err := q.GetPlan(ctx, *sub.PlanCode)
		free = err == nil && IsFree(pl)
		return err
	})
	return free, err
}

// startFree moves the workspace onto a free plan. A paid plan has to be cancelled and run out
// first, so no paid time is lost; a checkout not yet paid for is stopped.
func (s *Service) startFree(w http.ResponseWriter, r *http.Request, plan dbq.Plan, sub dbq.Subscription, now time.Time) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	onFree, err := s.onFreePlan(ctx, p.TenantID, sub)
	if err != nil {
		return err
	}
	paid := sub.PlanCode != nil && !onFree &&
		(sub.Status == dbq.SubscriptionStatusActive || sub.Status == dbq.SubscriptionStatusPastDue)
	switch {
	case onFree && sub.Status == dbq.SubscriptionStatusActive && *sub.PlanCode == plan.Code:
		return httpx.NewError(http.StatusConflict, "conflict", "You are already on this plan.")
	case paid && sub.CancelAtPeriodEnd:
		return httpx.NewError(http.StatusConflict, "conflict",
			"Your plan is set to end on "+sub.CurrentPeriodEnd.In(ist).Format("2 Jan 2006")+". Choose "+plan.Name+" after that date.")
	case paid:
		return httpx.NewError(http.StatusConflict, "conflict",
			"Cancel your current plan first. You can choose "+plan.Name+" once it ends.")
	}
	// An unpaid checkout (from the trial or from the free plan) must not charge later.
	if sub.ProviderSubscriptionID != nil && sub.Status != dbq.SubscriptionStatusCancelled && s.rp.Configured() {
		if _, err := s.rp.Cancel(ctx, *sub.ProviderSubscriptionID, false); err != nil {
			var re *razorpay.Error
			if !errors.As(err, &re) || re.HTTPStatus != http.StatusBadRequest {
				return s.providerError(err)
			}
			s.log.Info("earlier razorpay subscription not cancelled", "subscription", *sub.ProviderSubscriptionID, "description", re.Description)
		}
	}
	var out dbq.Subscription
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		out, err = q.StartFreePlan(ctx, dbq.StartFreePlanParams{
			PlanCode: &plan.Code, PeriodStart: now, PeriodEnd: now.AddDate(0, 1, 0), TenantID: p.TenantID})
		return err
	})
	if err != nil {
		return err
	}
	if err := s.audit(r, "subscription.free", plan.Code); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subscription": SubscriptionView(out, now)})
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
				desc := fmt.Sprintf("Ecogo Connect: %d extra team seat%s", extra, plural(extra))
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
		// A replacement subscription (a plan change Razorpay could not make in place).
		if sub.PendingProviderSubscriptionID != nil && *sub.PendingProviderSubscriptionID == rs.ID {
			switch {
			case endings[ev.Event]:
				// Never paid: the current subscription carries on unchanged.
				return q.ClearPendingSubscription(ctx, tenantID)
			case ev.Event == "subscription.authenticated" && !sub.PendingReplaceNow && sub.ProviderSubscriptionID != nil:
				// Approved to start when the paid period ends: the current one must not renew.
				if err := s.cancelReplaced(ctx, *sub.ProviderSubscriptionID, true); err != nil {
					return err
				}
				return q.SetCancelAtPeriodEnd(ctx, dbq.SetCancelAtPeriodEndParams{Cancel: true, TenantID: tenantID})
			case ev.Event == "subscription.activated" || ev.Event == "subscription.charged":
				// Paid: it takes over, and the one it replaces stops now (an upgrade) or has
				// already been set to stop at its cycle end.
				if sub.ProviderSubscriptionID != nil && sub.PendingReplaceNow {
					if err := s.cancelReplaced(ctx, *sub.ProviderSubscriptionID, false); err != nil {
						return err
					}
					if err := s.refundUnused(ctx, q, tenantID, *sub.ProviderSubscriptionID); err != nil {
						return err
					}
				}
				if sub, err = q.PromotePendingSubscription(ctx, tenantID); err != nil {
					return err
				}
			default:
				return nil
			}
		} else if sub.ProviderSubscriptionID == nil || *sub.ProviderSubscriptionID != rs.ID {
			// A subscription this workspace has moved on from, e.g. the one a replacement
			// cancelled.
			s.log.Info("razorpay event for a replaced subscription", "event", ev.Event, "subscription", rs.ID)
			return nil
		}
		// A checkout started from the free plan (or a granted one) that ends unpaid leaves the
		// workspace on it.
		if endings[ev.Event] && sub.PlanCode != nil {
			if cur, err := q.GetPlan(ctx, *sub.PlanCode); err != nil {
				return err
			} else if IsFree(cur) || sub.AdminGranted {
				return q.DropProviderSubscription(ctx, dbq.DropProviderSubscriptionParams{TenantID: tenantID, ProviderSubscriptionID: &rs.ID})
			}
		}
		next := Transition(sub, ev.Event, rs)
		if next == nil {
			return nil
		}
		planName := "Ecogo Connect plan"
		if rs.PlanID != "" {
			pl, err := q.GetPlanByRazorpayID(ctx, rs.PlanID)
			if err == nil {
				next.PlanCode = &pl.Code
				interval := "month"
				if pl.YearlyRazorpayPlanID != nil && *pl.YearlyRazorpayPlanID == rs.PlanID {
					interval = "year"
				}
				next.BillingInterval = &interval
				planName = pl.Name + " plan"
				if interval == "year" {
					planName += " (yearly)"
				}
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
			desc := "Ecogo Connect: " + planName
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

// endings are the Razorpay events that end a subscription.
var endings = map[string]bool{"subscription.cancelled": true, "subscription.completed": true, "subscription.expired": true}

// syncEvents maps the Razorpay subscription states that start or keep a paid plan onto the
// webhook event that reports them. Endings are left to the webhooks.
var syncEvents = map[string]string{
	"authenticated": "subscription.authenticated",
	"active":        "subscription.activated",
}

// sync asks Razorpay for the state of a checkout the webhooks have not confirmed yet (payment
// pending, or a workspace that cannot send), so a paid plan shows up even when a webhook is
// late or never arrives. Failures are logged; the webhooks stay the main path.
func (s *Service) sync(ctx context.Context, tenantID uuid.UUID) {
	if !s.rp.Configured() {
		return
	}
	var sub dbq.Subscription
	err := s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		sub, err = q.GetSubscription(ctx)
		return err
	})
	if err != nil {
		if !db.IsNotFound(err) {
			s.log.Warn("billing sync", "tenant_id", tenantID, "err", err)
		}
		return
	}
	if sub.ProviderSubscriptionID == nil || sub.Status == dbq.SubscriptionStatusCancelled {
		return
	}
	if sub.PlanCode != nil && Usable(sub, s.now()) {
		// A paid plan is confirmed; a free one may have a checkout waiting.
		free := sub.AdminGranted
		_ = s.db.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
			pl, err := q.GetPlan(ctx, *sub.PlanCode)
			free = free || (err == nil && IsFree(pl))
			return nil
		})
		if !free {
			return
		}
	}
	rs, err := s.rp.Subscription(ctx, *sub.ProviderSubscriptionID)
	if err != nil {
		s.log.Warn("billing sync: razorpay", "tenant_id", tenantID, "err", err)
		return
	}
	event, ok := syncEvents[rs.Status]
	if !ok {
		return
	}
	var ev razorpay.Event
	ev.Event = event
	ev.Payload.Subscription = &struct {
		Entity razorpay.Subscription `json:"entity"`
	}{Entity: rs}
	if err := s.apply(ctx, "", ev); err != nil {
		s.log.Warn("billing sync: apply", "tenant_id", tenantID, "err", err)
		return
	}
	s.log.Info("billing synced from razorpay", "tenant_id", tenantID, "subscription", rs.ID, "status", rs.Status)
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
		// A yearly or free plan's AI replies are per month, not per period.
		a.PeriodStart, a.PeriodEnd = UsageMonth(sub, time.Now())
	}
	return a, nil
}

// checkout is the reply that starts a payment. The dashboard opens Razorpay's checkout pop-up
// from the key and subscription id, which works without Razorpay's hosted page; payment_url
// stays for API clients.
// inPlaceRefused reports whether Razorpay refused to change a subscription's plan in place,
// as it does for subscriptions paid by UPI Autopay ("subscriptions cannot be updated when
// payment mode is upi").
func inPlaceRefused(err error) bool {
	var re *razorpay.Error
	return errors.As(err, &re) && re.HTTPStatus == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(re.Description), "cannot be updated")
}

// replaceSubscription changes plan by a second Razorpay subscription, for subscriptions Razorpay
// cannot change in place. An upgrade starts at once and the current subscription is cancelled
// when the new one is paid; any other change starts when the paid period ends. The owner
// approves the new subscription in Razorpay's checkout, as for a first payment.
func (s *Service) replaceSubscription(w http.ResponseWriter, r *http.Request, sub dbq.Subscription, plan dbq.Plan,
	rpPlan, interval string, upgrade bool, now time.Time) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := r.Context()
	total := cycles
	if interval == "year" {
		total = yearlyCycles
	}
	ns := razorpay.NewSubscription{PlanID: rpPlan, TotalCount: total,
		Notes: map[string]string{"tenant_id": p.TenantID.String(), "replaces": *sub.ProviderSubscriptionID}}
	if sub.ProviderCustomerID != nil {
		ns.CustomerID = *sub.ProviderCustomerID
	}
	if !upgrade && sub.CurrentPeriodEnd.After(now.Add(time.Hour)) {
		ns.StartAt = &sub.CurrentPeriodEnd
	}
	rs, err := s.rp.CreateSubscription(ctx, ns)
	if err != nil {
		return s.providerError(err)
	}
	err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.SetPendingSubscription(ctx, dbq.SetPendingSubscriptionParams{
			PendingProviderSubscriptionID: &rs.ID, ReplaceNow: upgrade, TenantID: p.TenantID})
	})
	if err != nil {
		return err
	}
	action := "subscription.replace"
	if upgrade {
		action = "subscription.upgrade"
	}
	if err := s.audit(r, action, plan.Code); err != nil {
		return err
	}
	out := s.checkout(rs)
	out["replacement"] = true
	out["upgraded"] = upgrade
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// cancelReplaced cancels the subscription a replacement takes over from. Razorpay answers 400
// when it has already ended, which is fine.
func (s *Service) cancelReplaced(ctx context.Context, id string, atCycleEnd bool) error {
	if _, err := s.rp.Cancel(ctx, id, atCycleEnd); err != nil {
		var re *razorpay.Error
		if !errors.As(err, &re) || re.HTTPStatus != http.StatusBadRequest {
			return err
		}
		s.log.Info("replaced razorpay subscription not cancelled", "subscription", id, "description", re.Description)
	}
	return nil
}

// refundUnused refunds the share of a replaced subscription's last payment that covers the
// time after now, so an upgrade does not charge twice for the same days. A refund Razorpay
// refuses is logged for staff and does not stop the upgrade; either way the audit log says
// what happened.
func (s *Service) refundUnused(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, oldID string) error {
	meta := map[string]any{"subscription": oldID}
	action := "subscription.refund_unused"
	inv, ok, err := s.rp.LastPaidInvoice(ctx, oldID)
	switch {
	case err != nil:
		action, meta["error"] = "subscription.refund_failed", err.Error()
	case !ok || inv.BillingStart == nil || inv.BillingEnd == nil || *inv.BillingEnd <= *inv.BillingStart:
		meta["skipped"] = "no paid invoice with a billing period"
	default:
		amount := UnusedShare(inv.AmountPaid, time.Unix(*inv.BillingStart, 0), time.Unix(*inv.BillingEnd, 0), s.now())
		meta["payment"], meta["amount_minor"] = inv.PaymentID, amount
		if amount < 100 { // under ₹1: nothing worth refunding
			meta["skipped"] = "nothing unused"
			break
		}
		ref, err := s.rp.RefundPayment(ctx, inv.PaymentID, amount, map[string]string{
			"tenant_id": tenantID.String(), "reason": "unused days after plan upgrade"})
		if err != nil {
			action, meta["error"] = "subscription.refund_failed", err.Error()
			s.log.Warn("refund of unused days failed", "tenant_id", tenantID, "payment", inv.PaymentID, "amount", amount, "err", err)
		} else {
			meta["refund"] = ref.ID
		}
	}
	raw, _ := json.Marshal(meta)
	target := "subscription"
	return q.InsertAuditLog(ctx, dbq.InsertAuditLogParams{
		TenantID: &tenantID, ActorType: dbq.ActorTypeSystem, Action: action,
		TargetType: &target, TargetID: &oldID, Metadata: raw,
	})
}

// UnusedShare is the part of amount (paise, rounded down) that pays for the time from now to
// the end of the period start..end.
func UnusedShare(amount int64, start, end, now time.Time) int64 {
	if !now.Before(end) || !end.After(start) || amount <= 0 {
		return 0
	}
	if now.Before(start) {
		return amount
	}
	return amount * int64(end.Sub(now)/time.Second) / int64(end.Sub(start)/time.Second)
}

// dropPending cancels a replacement subscription that was never paid.
func (s *Service) dropPending(ctx context.Context, tenantID uuid.UUID, sub dbq.Subscription) error {
	if sub.PendingProviderSubscriptionID == nil {
		return nil
	}
	if _, err := s.rp.Cancel(ctx, *sub.PendingProviderSubscriptionID, false); err != nil {
		var re *razorpay.Error
		if !errors.As(err, &re) || re.HTTPStatus != http.StatusBadRequest {
			return s.providerError(err)
		}
	}
	return s.db.InTenant(ctx, tenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		return q.ClearPendingSubscription(ctx, tenantID)
	})
}

func (s *Service) checkout(rs razorpay.Subscription) map[string]any {
	return map[string]any{"payment_url": rs.ShortURL, "razorpay_key_id": s.rp.KeyID(), "razorpay_subscription_id": rs.ID}
}
