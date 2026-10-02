package billing

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/razorpay"
)

var (
	gstin = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)
	ist   = time.FixedZone("IST", 5*3600+1800)
)

// Party is one side of an invoice, frozen when it is issued.
type Party struct {
	Name      string `json:"name"`
	GSTIN     string `json:"gstin,omitempty"`
	Address   string `json:"address,omitempty"`
	StateCode string `json:"state_code,omitempty"`
}

// Tax is the split of an amount that includes GST.
type Tax struct {
	Taxable, CGST, SGST, IGST int64
}

// SplitGST takes a GST-inclusive total (paise) and splits it at rateBP basis points. Inside one
// state the tax is shared between CGST and SGST (SGST takes the odd paisa); between states it
// is all IGST.
func SplitGST(total int64, rateBP int, sameState bool) Tax {
	taxable := (total*10000 + int64(10000+rateBP)/2) / int64(10000+rateBP)
	gst := total - taxable
	if !sameState {
		return Tax{Taxable: taxable, IGST: gst}
	}
	return Tax{Taxable: taxable, CGST: gst / 2, SGST: gst - gst/2}
}

// FinancialYear names the Indian financial year (April to March) containing t, e.g. 2026-27.
func FinancialYear(t time.Time) string {
	t = t.In(ist)
	y := t.Year()
	if t.Month() < time.April {
		y--
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

// ValidStateCode accepts the two-digit GST state codes.
func ValidStateCode(c string) bool {
	n, err := strconv.Atoi(c)
	return len(c) == 2 && err == nil && ((n >= 1 && n <= 38) || n == 97 || n == 99)
}

func (s *Service) sellerParty() Party {
	sl := s.seller
	return Party{Name: sl.Name, GSTIN: sl.GSTIN, Address: sl.Address, StateCode: sl.GSTIN[:2]}
}

// issueInvoice records the invoice for one confirmed payment. Run it inside the tenant, in the
// transaction that applied the webhook. It does nothing until the seller's GSTIN is set, and a
// payment is invoiced once.
func (s *Service) issueInvoice(ctx context.Context, q *dbq.Queries, tenantID uuid.UUID, pay *razorpay.Payment, description string, start, end *time.Time) error {
	if s.seller.GSTIN == "" || pay == nil || pay.ID == "" || pay.Amount <= 0 {
		return nil
	}
	if pay.Currency != "" && pay.Currency != "INR" {
		s.log.Warn("not invoicing a payment in another currency", "payment", pay.ID, "currency", pay.Currency)
		return nil
	}
	if done, err := q.PaymentInvoiced(ctx, pay.ID); err != nil || done {
		return err
	}
	seller := s.sellerParty()
	buyer := Party{}
	prof, err := q.GetBillingProfile(ctx)
	switch {
	case err == nil:
		g := ""
		if prof.Gstin != nil {
			g = *prof.Gstin
		}
		buyer = Party{Name: prof.LegalName, GSTIN: g, Address: prof.Address, StateCode: prof.StateCode}
	case db.IsNotFound(err):
		t, err := q.GetTenant(ctx, tenantID)
		if err != nil {
			return err
		}
		buyer = Party{Name: t.Name}
		if t.LegalName != nil {
			buyer.Name = *t.LegalName
		}
	default:
		return err
	}
	place := buyer.StateCode
	if place == "" {
		place = seller.StateCode
	}
	tax := SplitGST(pay.Amount, s.seller.GSTRateBP, place == seller.StateCode)

	now := s.now()
	fy := FinancialYear(now)
	n, err := q.NextInvoiceNumber(ctx, fy)
	if err != nil {
		return err
	}
	sj, _ := json.Marshal(seller)
	bj, _ := json.Marshal(buyer)
	var sac *string
	if s.seller.SAC != "" {
		sac = &s.seller.SAC
	}
	_, err = q.InsertInvoice(ctx, dbq.InsertInvoiceParams{
		ID: db.NewID(), TenantID: tenantID, Number: fmt.Sprintf("ECO/%s/%06d", fy, n), RazorpayPaymentID: pay.ID,
		Description: description, PeriodStart: start, PeriodEnd: end, Sac: sac,
		TotalMinor: pay.Amount, TaxableMinor: tax.Taxable, GstRateBp: int32(s.seller.GSTRateBP),
		CgstMinor: tax.CGST, SgstMinor: tax.SGST, IgstMinor: tax.IGST, PlaceOfSupply: place,
		Seller: sj, Buyer: bj, IssuedAt: now,
	})
	return err
}

// Invoice is an issued invoice as the dashboard lists it.
type Invoice struct {
	ID          uuid.UUID `json:"id"`
	Number      string    `json:"number"`
	Description string    `json:"description"`
	TotalMinor  int64     `json:"total_minor"`
	IssuedAt    time.Time `json:"issued_at"`
}

func (s *Service) listInvoices(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := []Invoice{}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListInvoices(r.Context())
		for _, v := range rows {
			out = append(out, Invoice{ID: v.ID, Number: v.Number, Description: v.Description, TotalMinor: v.TotalMinor, IssuedAt: v.IssuedAt})
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// Profile is what appears as the buyer on invoices.
type Profile struct {
	LegalName string `json:"legal_name"`
	GSTIN     string `json:"gstin"`
	StateCode string `json:"state_code"`
	Address   string `json:"address"`
}

func (s *Service) getProfile(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := map[string]any{"profile": nil, "invoicing": s.seller.GSTIN != ""}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		prof, err := q.GetBillingProfile(r.Context())
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		g := ""
		if prof.Gstin != nil {
			g = *prof.Gstin
		}
		out["profile"] = Profile{LegalName: prof.LegalName, GSTIN: g, StateCode: prof.StateCode, Address: prof.Address}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) saveProfile(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	var req Profile
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.LegalName, req.Address = strings.TrimSpace(req.LegalName), strings.TrimSpace(req.Address)
	req.GSTIN, req.StateCode = strings.ToUpper(strings.TrimSpace(req.GSTIN)), strings.TrimSpace(req.StateCode)
	switch {
	case req.LegalName == "" || len(req.LegalName) > 200:
		return httpx.BadRequest("legal_name", "Enter the business name for invoices (up to 200 characters).")
	case req.Address == "" || len(req.Address) > 500:
		return httpx.BadRequest("address", "Enter the billing address (up to 500 characters).")
	case req.GSTIN != "" && !gstin.MatchString(req.GSTIN):
		return httpx.BadRequest("gstin", "Enter a valid 15-character GSTIN, or leave it empty.")
	}
	if req.GSTIN != "" {
		req.StateCode = req.GSTIN[:2] // the GSTIN decides the state
	}
	if !ValidStateCode(req.StateCode) {
		return httpx.BadRequest("state_code", "Choose the state for the invoice (two-digit GST state code).")
	}
	var g *string
	if req.GSTIN != "" {
		g = &req.GSTIN
	}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		if _, err := q.UpsertBillingProfile(r.Context(), dbq.UpsertBillingProfileParams{
			TenantID: p.TenantID, LegalName: req.LegalName, Gstin: g, StateCode: req.StateCode, Address: req.Address}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.audit(r, "billing.profile_save", ""); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, req)
	return nil
}

//go:embed invoice.html
var invoiceHTML string

var invoiceTmpl = template.Must(template.New("invoice").Funcs(template.FuncMap{
	"inr":      inr,
	"date":     func(t time.Time) string { return t.In(ist).Format("2 Jan 2006") },
	"rate":     func(bp int32) string { return strconv.FormatFloat(float64(bp)/100, 'f', -1, 64) },
	"halfrate": func(bp int32) string { return strconv.FormatFloat(float64(bp)/200, 'f', -1, 64) },
	"deref":    func(t *time.Time) time.Time { return *t },
}).Parse(invoiceHTML))

type invoiceView struct {
	dbq.Invoice
	Seller, Buyer Party
	Intra         bool
	PeriodStart   *time.Time
	PeriodEnd     *time.Time
	SAC           string
}

// view renders one invoice as a print-ready page (the browser can save it as a PDF).
func (s *Service) view(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var inv dbq.Invoice
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		inv, err = q.GetInvoice(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	v := invoiceView{Invoice: inv, PeriodStart: inv.PeriodStart, PeriodEnd: inv.PeriodEnd, Intra: inv.IgstMinor == 0}
	_ = json.Unmarshal(inv.Seller, &v.Seller)
	_ = json.Unmarshal(inv.Buyer, &v.Buyer)
	if inv.Sac != nil {
		v.SAC = *inv.Sac
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	return invoiceTmpl.Execute(w, v)
}

// inr formats paise as rupees with Indian digit grouping: 1234567 -> 12,345.67.
func inr(minor int64) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	whole, frac := minor/100, minor%100
	s := strconv.FormatInt(whole, 10)
	if len(s) > 3 {
		head, tail := s[:len(s)-3], s[len(s)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		if head != "" {
			parts = append([]string{head}, parts...)
		}
		s = strings.Join(parts, ",") + "," + tail
	}
	out := fmt.Sprintf("%s.%02d", s, frac)
	if neg {
		out = "-" + out
	}
	return out
}
