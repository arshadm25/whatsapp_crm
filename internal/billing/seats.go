package billing

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/razorpay"
)

// maxExtraSeats keeps a typo from creating an absurd Razorpay charge.
const maxExtraSeats = 500

// seats sets how many seats beyond the plan's included ones the workspace pays for. The first
// purchase returns Razorpay's payment page; later changes apply to the existing seat
// subscription: more seats at once (Razorpay charges the difference), fewer from the next cycle.
func (s *Service) seats(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req struct {
		Extra int `json:"extra"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if req.Extra < 0 || req.Extra > maxExtraSeats {
		return httpx.BadRequest("extra", fmt.Sprintf("Choose between 0 and %d extra seats.", maxExtraSeats))
	}
	if !s.rp.Configured() {
		return errPaymentsOff
	}
	ctx := r.Context()
	var (
		plan    dbq.Plan
		sub     dbq.Subscription
		members int32
	)
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if plan, err = q.CurrentPlan(ctx); db.IsNotFound(err) {
			return httpx.NewError(http.StatusConflict, "conflict", "Choose a plan before adding seats.")
		} else if err != nil {
			return err
		}
		if sub, err = q.GetSubscription(ctx); err != nil {
			return err
		}
		u, err := q.BillingUsage(ctx)
		members = u.Seats
		return err
	})
	if err != nil {
		return err
	}
	switch {
	case plan.ExtraSeatRazorpayPlanID == nil || plan.RazorpayPlanID == nil:
		return httpx.NewError(http.StatusConflict, "conflict", "Extra seats are not available on this plan.")
	case sub.Status != dbq.SubscriptionStatusActive || sub.ProviderSubscriptionID == nil || sub.ProviderCustomerID == nil:
		return httpx.NewError(http.StatusConflict, "conflict", "Extra seats can be added once your plan is paid for.")
	case sub.CancelAtPeriodEnd:
		return httpx.NewError(http.StatusConflict, "conflict", "Your plan is set to end, so seats cannot be changed.")
	case members > plan.IncludedSeats+int32(req.Extra):
		return httpx.NewError(http.StatusConflict, "seat_limit", fmt.Sprintf(
			"Your workspace has %d members. Remove some before reducing seats below %d.", members, members))
	}

	// A seat subscription that was never paid cannot be edited: start over.
	unpaid := sub.SeatProviderSubscriptionID != nil && sub.ExtraSeats == 0
	if unpaid {
		if err := s.cancelSeatSubscription(r, *sub.SeatProviderSubscriptionID, false); err != nil {
			return err
		}
		if err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			return q.SetSeatSubscription(ctx, dbq.SetSeatSubscriptionParams{TenantID: p.TenantID})
		}); err != nil {
			return err
		}
		sub.SeatProviderSubscriptionID = nil
	}

	switch {
	case sub.SeatProviderSubscriptionID == nil:
		if req.Extra == 0 {
			httpx.JSON(w, http.StatusOK, map[string]any{"scheduled": false})
			return nil
		}
		rs, err := s.rp.CreateSubscription(ctx, razorpay.NewSubscription{
			PlanID: *plan.ExtraSeatRazorpayPlanID, CustomerID: *sub.ProviderCustomerID, TotalCount: cycles,
			Quantity: req.Extra, Notes: map[string]string{"tenant_id": p.TenantID.String(), "kind": "seats"},
		})
		if err != nil {
			return s.providerError(err)
		}
		if err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			return q.SetSeatSubscription(ctx, dbq.SetSeatSubscriptionParams{TenantID: p.TenantID, SeatProviderSubscriptionID: &rs.ID})
		}); err != nil {
			return err
		}
		if err := s.audit(r, "subscription.seats_checkout", fmt.Sprint(req.Extra)); err != nil {
			return err
		}
		httpx.JSON(w, http.StatusOK, s.checkout(rs))
	case req.Extra == 0:
		if err := s.cancelSeatSubscription(r, *sub.SeatProviderSubscriptionID, true); err != nil {
			return err
		}
		if err := s.audit(r, "subscription.seats_cancel", ""); err != nil {
			return err
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"scheduled": true})
	case int32(req.Extra) == sub.ExtraSeats:
		httpx.JSON(w, http.StatusOK, map[string]any{"scheduled": false})
	default:
		more := int32(req.Extra) > sub.ExtraSeats
		if _, err := s.rp.ChangeQuantity(ctx, *sub.SeatProviderSubscriptionID, req.Extra, more); err != nil {
			return s.providerError(err)
		}
		if err := s.audit(r, "subscription.seats_change", fmt.Sprint(req.Extra)); err != nil {
			return err
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"scheduled": !more})
	}
	return nil
}

// cancelSeatSubscription cancels on Razorpay, treating "already ended" (400) as done.
func (s *Service) cancelSeatSubscription(r *http.Request, id string, atCycleEnd bool) error {
	if _, err := s.rp.Cancel(r.Context(), id, atCycleEnd); err != nil {
		var re *razorpay.Error
		if errors.As(err, &re) && re.HTTPStatus == http.StatusBadRequest {
			s.log.Info("seat subscription not cancelled", "subscription", id, "description", re.Description)
			return nil
		}
		return s.providerError(err)
	}
	return nil
}

// seatEvent applies a webhook for a seat subscription: extra_seats follows its quantity while
// it is paid, and drops to zero when it ends.
func seatEvent(event string, rs razorpay.Subscription) (extra int32, ended, ok bool) {
	switch event {
	case "subscription.authenticated", "subscription.activated", "subscription.charged",
		"subscription.resumed", "subscription.updated":
		q := rs.Quantity
		if q < 1 {
			q = 1
		}
		return int32(q), false, true
	case "subscription.halted", "subscription.cancelled", "subscription.completed", "subscription.expired":
		return 0, true, true
	}
	return 0, false, false
}
