package admin

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/metafees"
)

var countryCode = regexp.MustCompile(`^([A-Z]{2}|\*)$`)

// RateRow is a line of Meta's rate card.
type RateRow struct {
	ID             uuid.UUID `json:"id"`
	Category       string    `json:"category"`
	Country        string    `json:"country"`
	RateHundredths int64     `json:"rate_hundredths"`
	EffectiveFrom  string    `json:"effective_from"`
}

func rateRow(r dbq.MetaRateCard) RateRow {
	return RateRow{ID: r.ID, Category: r.Category, Country: r.Country, RateHundredths: r.RateHundredths, EffectiveFrom: r.EffectiveFrom.Time.Format("2006-01-02")}
}

func (s *Service) listMetaRates(w http.ResponseWriter, r *http.Request) error {
	out := []RateRow{}
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListMetaRates(r.Context())
		for _, v := range rows {
			out = append(out, rateRow(v))
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// saveMetaRate adds a rate, or replaces the rate that starts the same day. History stays, so a
// past month is always priced with the rates in force then.
func (s *Service) saveMetaRate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Category       string `json:"category"`
		Country        string `json:"country"`
		RateHundredths int64  `json:"rate_hundredths"`
		EffectiveFrom  string `json:"effective_from"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Country = strings.ToUpper(strings.TrimSpace(req.Country))
	from, err := time.Parse("2006-01-02", req.EffectiveFrom)
	switch {
	case req.Category != "marketing" && req.Category != "utility" && req.Category != "authentication":
		return httpx.BadRequest("category", "Choose marketing, utility or authentication. Service messages are free.")
	case !countryCode.MatchString(req.Country):
		return httpx.BadRequest("country", "Use a two-letter country code such as IN, or * for every other country.")
	case req.RateHundredths < 0 || req.RateHundredths > 100_000_000:
		return httpx.BadRequest("rate_hundredths", "The rate is in hundredths of a paisa per message and cannot be negative.")
	case err != nil:
		return httpx.BadRequest("effective_from", "Enter the date the rate starts, as YYYY-MM-DD.")
	}
	var out dbq.MetaRateCard
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		out, err = q.UpsertMetaRate(r.Context(), dbq.UpsertMetaRateParams{ID: db.NewID(), Category: req.Category, Country: req.Country,
			RateHundredths: req.RateHundredths, EffectiveFrom: pgtype.Date{Time: from, Valid: true}})
		if err != nil {
			return err
		}
		return s.record(r, q, nil, "meta_rate.save", "meta_rate", out.ID.String(), nil)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, rateRow(out))
	return nil
}

func (s *Service) deleteMetaRate(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		n, err := q.DeleteMetaRate(r.Context(), id)
		if err != nil {
			return err
		}
		if n == 0 {
			return httpx.ErrNotFound
		}
		return s.record(r, q, nil, "meta_rate.delete", "meta_rate", id.String(), nil)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// setMetaPaymentMode switches who pays Meta for a workspace's messages. The statement job bills
// from the moment of the switch, so earlier usage stays with the workspace's own Meta invoice.
func (s *Service) setMetaPaymentMode(w http.ResponseWriter, r *http.Request) error {
	id, err := tenantID(r)
	if err != nil {
		return err
	}
	var req struct {
		Mode   string `json:"mode"`
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	why := strings.TrimSpace(req.Reason)
	switch {
	case req.Mode != metafees.ModeDirect && req.Mode != metafees.ModeThroughUs:
		return httpx.BadRequest("mode", "Choose direct (the workspace pays Meta) or through_us (Ecogo re-bills Meta's fees).")
	case len(why) < 5 || len(why) > 500:
		return httpx.BadRequest("reason", "Give a reason of 5 to 500 characters; it is kept in the audit log.")
	}
	var t dbq.Tenant
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		cur, err := q.GetTenant(r.Context(), id)
		if db.IsNotFound(err) {
			return httpx.NewError(http.StatusNotFound, "not_found", "Workspace not found.")
		}
		if err != nil {
			return err
		}
		if cur.MetaPaymentMode == req.Mode {
			t = cur
			return nil
		}
		if t, err = q.SetMetaPaymentMode(r.Context(), dbq.SetMetaPaymentModeParams{ID: id, Mode: req.Mode}); err != nil {
			return err
		}
		return s.record(r, q, &id, "tenant.meta_payment_mode", "tenant", id.String(), &why)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, tenantView(t))
	return nil
}

func (s *Service) listMetaFeeStatements(w http.ResponseWriter, r *http.Request) error {
	arg := dbq.AdminListMetaFeeStatementsParams{Lim: listLimit}
	if v := r.URL.Query().Get("tenant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("tenant_id", "Not a valid workspace id.")
		}
		arg.TenantID = &id
	}
	if v := r.URL.Query().Get("status"); v != "" {
		if v != "due" && v != "paid" && v != "void" {
			return httpx.BadRequest("status", "Use due, paid or void.")
		}
		arg.Status = &v
	}
	var rows []dbq.AdminListMetaFeeStatementsRow
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		rows, err = q.AdminListMetaFeeStatements(r.Context(), arg)
		return err
	})
	if err != nil {
		return err
	}
	out := make([]metafees.Statement, len(rows))
	var due int64
	for i, v := range rows {
		name := v.TenantName
		out[i] = metafees.StatementView(statementOf(v), &name)
		if v.Status == "due" {
			due += v.TotalMinor
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out, "due_minor": due, "truncated": len(rows) == listLimit})
	return nil
}

func statementOf(v dbq.AdminListMetaFeeStatementsRow) dbq.MetaFeeStatement {
	return dbq.MetaFeeStatement{ID: v.ID, TenantID: v.TenantID, Month: v.Month, Number: v.Number, Lines: v.Lines, Subscription: v.Subscription,
		FeeMinor: v.FeeMinor, MarkupBp: v.MarkupBp, MarkupMinor: v.MarkupMinor, TaxableMinor: v.TaxableMinor, GstRateBp: v.GstRateBp,
		CgstMinor: v.CgstMinor, SgstMinor: v.SgstMinor, IgstMinor: v.IgstMinor, TotalMinor: v.TotalMinor, PlaceOfSupply: v.PlaceOfSupply,
		Seller: v.Seller, Buyer: v.Buyer, Status: v.Status, PaidAt: v.PaidAt, PaymentReference: v.PaymentReference, IssuedAt: v.IssuedAt}
}

func (s *Service) viewMetaFeeStatement(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var m dbq.MetaFeeStatement
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		m, err = q.AdminGetMetaFeeStatement(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	return metafees.WriteStatement(w, m)
}

// setMetaFeeStatementStatus records that a statement was paid (outside the platform, for example by
// bank transfer or UPI), or voids it. Voiding does not release the month: no new statement is issued for it.
func (s *Service) setMetaFeeStatementStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var req struct {
		Status    string `json:"status"`
		Reference string `json:"payment_reference"`
		Reason    string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	ref := strings.TrimSpace(req.Reference)
	why := strings.TrimSpace(req.Reason)
	switch {
	case req.Status != "paid" && req.Status != "due" && req.Status != "void":
		return httpx.BadRequest("status", "Use paid, due or void.")
	case req.Status == "paid" && (ref == "" || len(ref) > 100):
		return httpx.BadRequest("payment_reference", "Enter the payment reference, such as the UPI or bank transaction ID.")
	case len(why) < 5 || len(why) > 500:
		return httpx.BadRequest("reason", "Give a reason of 5 to 500 characters; it is kept in the audit log.")
	}
	var refp *string
	if req.Status == "paid" {
		refp = &ref
	}
	var cur dbq.MetaFeeStatement
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		cur, err = q.AdminGetMetaFeeStatement(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	// The row is in the workspace's own scope, so it is changed inside it.
	var out dbq.MetaFeeStatement
	err = s.db.InTenant(r.Context(), cur.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		out, err = q.SetMetaFeeStatementPaid(r.Context(), dbq.SetMetaFeeStatementPaidParams{ID: id, Status: req.Status, PaymentReference: refp})
		if err != nil {
			return err
		}
		return s.record(r, q, &cur.TenantID, "meta_fee_statement."+req.Status, "meta_fee_statement", id.String(), &why)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, metafees.StatementView(out, nil))
	return nil
}
