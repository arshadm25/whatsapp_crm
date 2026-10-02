package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

const (
	listLimit   = 500
	exportLimit = 20000
)

var ist = time.FixedZone("IST", 5*3600+1800)

// InvoiceRow is an invoice with its workspace, for the admin list and export.
type InvoiceRow struct {
	ID            uuid.UUID `json:"id"`
	TenantID      uuid.UUID `json:"tenant_id"`
	TenantName    string    `json:"tenant_name"`
	Number        string    `json:"number"`
	Description   string    `json:"description"`
	BuyerName     string    `json:"buyer_name"`
	BuyerGSTIN    string    `json:"buyer_gstin"`
	PlaceOfSupply string    `json:"place_of_supply"`
	TaxableMinor  int64     `json:"taxable_minor"`
	CGSTMinor     int64     `json:"cgst_minor"`
	SGSTMinor     int64     `json:"sgst_minor"`
	IGSTMinor     int64     `json:"igst_minor"`
	TotalMinor    int64     `json:"total_minor"`
	PaymentID     string    `json:"razorpay_payment_id"`
	IssuedAt      time.Time `json:"issued_at"`
	Emailed       bool      `json:"emailed"`
}

// invoiceFilter reads from and to (dates in India, to inclusive) and tenant_id.
func invoiceFilter(r *http.Request) (dbq.AdminListInvoicesParams, error) {
	var arg dbq.AdminListInvoicesParams
	day := func(name string, plus int) (*time.Time, error) {
		v := r.URL.Query().Get(name)
		if v == "" {
			return nil, nil
		}
		t, err := time.ParseInLocation("2006-01-02", v, ist)
		if err != nil {
			return nil, httpx.BadRequest(name, name+" must be a date such as 2026-10-31.")
		}
		t = t.AddDate(0, 0, plus)
		return &t, nil
	}
	var err error
	if arg.FromAt, err = day("from", 0); err != nil {
		return arg, err
	}
	if arg.ToAt, err = day("to", 1); err != nil {
		return arg, err
	}
	arg.TenantID, err = optionalUUID(r, "tenant_id")
	return arg, err
}

func invoiceRows(rows []dbq.AdminListInvoicesRow) []InvoiceRow {
	out := make([]InvoiceRow, len(rows))
	for i, v := range rows {
		var buyer billing.Party
		_ = json.Unmarshal(v.Buyer, &buyer)
		out[i] = InvoiceRow{ID: v.ID, TenantID: v.TenantID, TenantName: v.TenantName, Number: v.Number,
			Description: v.Description, BuyerName: buyer.Name, BuyerGSTIN: buyer.GSTIN, PlaceOfSupply: v.PlaceOfSupply,
			TaxableMinor: v.TaxableMinor, CGSTMinor: v.CgstMinor, SGSTMinor: v.SgstMinor, IGSTMinor: v.IgstMinor,
			TotalMinor: v.TotalMinor, PaymentID: v.RazorpayPaymentID, IssuedAt: v.IssuedAt, Emailed: v.EmailedAt != nil}
	}
	return out
}

func (s *Service) listInvoices(w http.ResponseWriter, r *http.Request) error {
	arg, err := invoiceFilter(r)
	if err != nil {
		return err
	}
	arg.Lim = listLimit
	var rows []dbq.AdminListInvoicesRow
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		rows, err = q.AdminListInvoices(r.Context(), arg)
		return err
	})
	if err != nil {
		return err
	}
	var total int64
	for _, v := range rows {
		total += v.TotalMinor
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": invoiceRows(rows), "total_minor": total, "truncated": len(rows) == listLimit})
	return nil
}

// exportInvoices downloads the filtered invoices as CSV for the accountant, and records it.
func (s *Service) exportInvoices(w http.ResponseWriter, r *http.Request) error {
	arg, err := invoiceFilter(r)
	if err != nil {
		return err
	}
	arg.Lim = exportLimit
	var rows []dbq.AdminListInvoicesRow
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if rows, err = q.AdminListInvoices(r.Context(), arg); err != nil {
			return err
		}
		return s.record(r, q, arg.TenantID, "invoices.export", "invoices", fmt.Sprintf("%d rows", len(rows)), nil)
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ecogo-invoices.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Invoice number", "Date", "Workspace", "Buyer", "Buyer GSTIN", "Place of supply",
		"Description", "Taxable value", "CGST", "SGST", "IGST", "Total", "Razorpay payment"})
	money := func(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }
	for _, v := range invoiceRows(rows) {
		_ = cw.Write([]string{safeCell(v.Number), v.IssuedAt.In(ist).Format("2006-01-02"), safeCell(v.TenantName),
			safeCell(v.BuyerName), safeCell(v.BuyerGSTIN), v.PlaceOfSupply, safeCell(v.Description), money(v.TaxableMinor),
			money(v.CGSTMinor), money(v.SGSTMinor), money(v.IGSTMinor), money(v.TotalMinor), safeCell(v.PaymentID)})
	}
	cw.Flush()
	return nil
}

// safeCell stops spreadsheet programs treating text a customer chose (names) as a formula.
func safeCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func (s *Service) viewInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var inv dbq.Invoice
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		inv, err = q.AdminGetInvoice(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	return billing.WriteInvoice(w, inv)
}
