package metafees

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

type Service struct {
	db       *db.DB
	seller   config.Seller
	markupBP int
	log      *slog.Logger
	now      func() time.Time
}

func NewService(d *db.DB, seller config.Seller, markupBP int, log *slog.Logger) *Service {
	return &Service{db: d, seller: seller, markupBP: markupBP, log: log, now: time.Now}
}

// Routes mounts /internal/billing/meta-fees for the dashboard. Only owners see billing.
func (s *Service) Routes(r chi.Router) {
	r.Use(auth.RequireRole(dbq.MemberRoleOwner))
	r.Get("/", httpx.Handler(s.log, s.overview))
	r.Get("/statements/{id}/view", httpx.Handler(s.log, s.view))
}

// Statement is a monthly statement as the dashboard and the console list it.
type Statement struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	TenantName       *string    `json:"tenant_name,omitempty"`
	Month            string     `json:"month"` // 2026-09
	Number           string     `json:"number"`
	FeeMinor         int64      `json:"fee_minor"`
	MarkupMinor      int64      `json:"markup_minor"`
	TaxableMinor     int64      `json:"taxable_minor"`
	GSTMinor         int64      `json:"gst_minor"`
	TotalMinor       int64      `json:"total_minor"`
	Status           string     `json:"status"`
	PaidAt           *time.Time `json:"paid_at"`
	PaymentReference *string    `json:"payment_reference"`
	IssuedAt         time.Time  `json:"issued_at"`
	Lines            []Line     `json:"lines"`
}

// StatementView converts a stored statement.
func StatementView(m dbq.MetaFeeStatement, tenantName *string) Statement {
	var lines []Line
	_ = json.Unmarshal(m.Lines, &lines)
	if lines == nil {
		lines = []Line{}
	}
	return Statement{
		ID: m.ID, TenantID: m.TenantID, TenantName: tenantName, Month: m.Month.Time.Format("2006-01"), Number: m.Number,
		FeeMinor: m.FeeMinor, MarkupMinor: m.MarkupMinor, TaxableMinor: m.TaxableMinor,
		GSTMinor: m.CgstMinor + m.SgstMinor + m.IgstMinor, TotalMinor: m.TotalMinor, Status: m.Status,
		PaidAt: m.PaidAt, PaymentReference: m.PaymentReference, IssuedAt: m.IssuedAt, Lines: lines,
	}
}

// Overview is what the dashboard shows about Meta's fees.
type Overview struct {
	Mode        string       `json:"mode"`
	Since       *time.Time   `json:"since"`
	Invoicing   bool         `json:"invoicing"`
	MonthToDate *MonthToDate `json:"month_to_date"`
	Statements  []Statement  `json:"statements"`
}

// MonthToDate estimates the current month's statement.
type MonthToDate struct {
	Month         string `json:"month"`
	Lines         []Line `json:"lines"`
	FeeMinor      int64  `json:"fee_minor"`
	MarkupMinor   int64  `json:"markup_minor"`
	EstimateMinor int64  `json:"estimate_minor"` // with GST, as if issued now
	Unrated       int64  `json:"unrated_messages"`
}

func (s *Service) overview(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := Overview{Statements: []Statement{}, Invoicing: s.seller.GSTIN != ""}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		t, err := q.GetTenant(r.Context(), p.TenantID)
		if err != nil {
			return err
		}
		out.Mode, out.Since = t.MetaPaymentMode, t.MetaPaymentModeSince
		rows, err := q.ListMetaFeeStatements(r.Context())
		if err != nil {
			return err
		}
		for _, m := range rows {
			out.Statements = append(out.Statements, StatementView(m, nil))
		}
		if t.MetaPaymentMode != ModeThroughUs {
			return nil
		}
		rates, err := q.ListMetaRates(r.Context())
		if err != nil {
			return err
		}
		loc := analytics.Location(t.Timezone)
		now := s.now().In(loc)
		month := MonthStart(now)
		from := month
		if t.MetaPaymentModeSince != nil {
			if since := t.MetaPaymentModeSince.In(loc); since.After(from) {
				from = since
			}
		}
		use, err := Compute(r.Context(), q, Card(rates), from, now)
		if err != nil {
			return err
		}
		tot := Price(use.FeeMinor, s.markupBP, s.seller.GSTRateBP, true)
		out.MonthToDate = &MonthToDate{Month: month.Format("2006-01"), Lines: use.Lines, FeeMinor: use.FeeMinor,
			MarkupMinor: tot.MarkupMinor, EstimateMinor: tot.TotalMinor, Unrated: use.Unrated}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) view(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	var m dbq.MetaFeeStatement
	err = s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		m, err = q.GetMetaFeeStatement(r.Context(), id)
		return err
	})
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	return WriteStatement(w, m)
}

//go:embed statement.html
var statementHTML string

var statementTmpl = template.Must(template.New("statement").Funcs(template.FuncMap{
	"inr":      billing.INR,
	"date":     func(t time.Time) string { return t.In(time.FixedZone("IST", 5*3600+1800)).Format("2 Jan 2006") },
	"month":    func(t time.Time) string { return t.Format("January 2006") },
	"rate":     func(bp int32) string { return strconv.FormatFloat(float64(bp)/100, 'f', -1, 64) },
	"halfrate": func(bp int32) string { return strconv.FormatFloat(float64(bp)/200, 'f', -1, 64) },
	"perMsg":   func(h int64) string { return strconv.FormatFloat(float64(h)/10000, 'f', 4, 64) },
	"label":    func(s string) string { return strings.ToUpper(s[:1]) + s[1:] },
}).Parse(statementHTML))

type statementPage struct {
	dbq.MetaFeeStatement
	Seller, Buyer billing.Party
	Intra         bool
	Lines         []Line
	Subscription  []struct {
		Number     string `json:"number"`
		TotalMinor int64  `json:"total_minor"`
	}
	SubscriptionTotal int64
}

// WriteStatement renders a statement as a print-ready page (the browser can save it as a PDF).
func WriteStatement(w http.ResponseWriter, m dbq.MetaFeeStatement) error {
	v := statementPage{MetaFeeStatement: m, Intra: m.IgstMinor == 0}
	_ = json.Unmarshal(m.Seller, &v.Seller)
	_ = json.Unmarshal(m.Buyer, &v.Buyer)
	_ = json.Unmarshal(m.Lines, &v.Lines)
	_ = json.Unmarshal(m.Subscription, &v.Subscription)
	for _, s := range v.Subscription {
		v.SubscriptionTotal += s.TotalMinor
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	return statementTmpl.Execute(w, v)
}
