package billing

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// Unlimited is what MessageRoom and ContactRoom return when the plan sets no limit.
const Unlimited = -1

// IsFree reports whether a plan needs no payment.
func IsFree(p dbq.Plan) bool { return p.PriceMinor == 0 }

// MonthlyEquivalent is what a plan costs per month on the given billing interval, in paise,
// for telling an upgrade from a downgrade.
func MonthlyEquivalent(p dbq.Plan, interval string) int64 {
	if interval == "year" && p.YearlyPriceMinor != nil {
		return *p.YearlyPriceMinor / 12
	}
	return p.PriceMinor
}

// UsageMonth is the month a plan's monthly allowances (messages, AI replies) count over: the
// month of the subscription that contains now, counted from the day the period started. For a
// monthly plan this is the billing period; a yearly or free plan gets a fresh allowance on the
// same day each month.
func UsageMonth(sub dbq.Subscription, now time.Time) (start, end time.Time) {
	start = sub.CurrentPeriodStart
	if now.Before(start) {
		return start, start.AddDate(0, 1, 0)
	}
	// Whole months since the start, then step forward or back once if the day of the month
	// (or a short month) puts now on the other side.
	months := (now.Year()-start.Year())*12 + int(now.Month()-start.Month())
	s := start.AddDate(0, months, 0)
	if s.After(now) {
		months--
		s = start.AddDate(0, months, 0)
	}
	return s, start.AddDate(0, months+1, 0)
}

func planAndSub(ctx context.Context, q *dbq.Queries) (*dbq.Plan, dbq.Subscription, error) {
	sub, err := q.GetSubscription(ctx)
	if err != nil {
		return nil, sub, err
	}
	if sub.PlanCode == nil {
		return nil, sub, nil
	}
	plan, err := q.GetPlan(ctx, *sub.PlanCode)
	if err != nil {
		return nil, sub, err
	}
	return &plan, sub, nil
}

// MessageRoom is how many more messages the workspace may send this month, or Unlimited. A
// trial with no plan chosen has no limit. Run it inside the tenant.
func MessageRoom(ctx context.Context, q *dbq.Queries, now time.Time) (int32, error) {
	plan, sub, err := planAndSub(ctx, q)
	if db.IsNotFound(err) {
		return Unlimited, nil
	}
	if err != nil {
		return 0, err
	}
	if plan == nil || plan.MessagesPerMonth == nil {
		return Unlimited, nil
	}
	start, _ := UsageMonth(sub, now)
	used, err := q.OutboundMessagesSince(ctx, start)
	if err != nil {
		return 0, err
	}
	return max(*plan.MessagesPerMonth-used, 0), nil
}

// ErrMessageLimit is returned when the month's messages are used up.
func ErrMessageLimit(ctx context.Context, q *dbq.Queries, now time.Time) error {
	msg := "Your plan's messages for this month are used up. Choose a larger plan under Settings, Billing."
	if plan, sub, err := planAndSub(ctx, q); err == nil && plan != nil && plan.MessagesPerMonth != nil {
		_, end := UsageMonth(sub, now)
		msg = fmt.Sprintf("Your %s plan includes %d messages a month and they are used up until %s. Choose a larger plan under Settings, Billing.",
			plan.Name, *plan.MessagesPerMonth, end.In(ist).Format("2 Jan 2006"))
	}
	return httpx.NewError(http.StatusPaymentRequired, "message_limit", msg)
}

// ContactRoom is how many more contacts the workspace may add, or Unlimited. Contacts created
// by customers writing in are always kept and are not refused. Run it inside the tenant.
func ContactRoom(ctx context.Context, q *dbq.Queries) (int32, error) {
	plan, _, err := planAndSub(ctx, q)
	if db.IsNotFound(err) {
		return Unlimited, nil
	}
	if err != nil {
		return 0, err
	}
	if plan == nil || plan.ContactsLimit == nil {
		return Unlimited, nil
	}
	used, err := q.ContactsInUse(ctx)
	if err != nil {
		return 0, err
	}
	return max(*plan.ContactsLimit-used, 0), nil
}

// ErrContactLimit is returned when adding a contact would go over the plan's contacts.
var ErrContactLimit = httpx.NewError(http.StatusConflict, "contact_limit",
	"Your plan's contact limit is reached. Remove contacts or choose a larger plan under Settings, Billing.")

// ErrPaidThroughRazorpay is returned when staff try to set the plan of a workspace that pays
// Razorpay: the plan would change here but not in Razorpay.
var ErrPaidThroughRazorpay = httpx.NewError(http.StatusConflict, "paid_plan",
	"This workspace pays for its plan through Razorpay. The owner changes it under Settings, Billing, or cancels it first.")

// GrantPlan puts the workspace on a plan without payment, from the admin console. until, when
// set, is when the plan stops; otherwise it runs until the owner pays for a plan. A workspace
// that pays Razorpay, or has more numbers or members than the plan includes, is refused. Run
// it inside the tenant.
func GrantPlan(ctx context.Context, q *dbq.Queries, plan dbq.Plan, until *time.Time, now time.Time) (dbq.Subscription, error) {
	sub, err := q.GetSubscription(ctx)
	if err != nil {
		return sub, err
	}
	if sub.ProviderSubscriptionID != nil && sub.Status != dbq.SubscriptionStatusCancelled {
		return sub, ErrPaidThroughRazorpay
	}
	if used, err := q.NumbersInUse(ctx, ""); err != nil {
		return sub, err
	} else if used > plan.IncludedNumbers {
		return sub, httpx.NewError(http.StatusConflict, "plan_limit", fmt.Sprintf(
			"The workspace has %d WhatsApp numbers connected and %s includes %d.", used, plan.Name, plan.IncludedNumbers))
	}
	if u, err := q.BillingUsage(ctx); err != nil {
		return sub, err
	} else if u.Seats > plan.IncludedSeats+sub.ExtraSeats {
		return sub, httpx.NewError(http.StatusConflict, "seat_limit", fmt.Sprintf(
			"The workspace has %d members and %s includes %d seats.", u.Seats, plan.Name, plan.IncludedSeats+sub.ExtraSeats))
	}
	end := now.AddDate(0, 1, 0)
	if until != nil {
		end = *until
	}
	return q.GrantPlan(ctx, dbq.GrantPlanParams{PlanCode: &plan.Code, HasEnd: until != nil, PeriodStart: now,
		PeriodEnd: end, TenantID: sub.TenantID})
}
