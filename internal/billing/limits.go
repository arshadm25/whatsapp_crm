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
