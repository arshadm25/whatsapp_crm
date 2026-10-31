package admin

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

var sacPattern = regexp.MustCompile(`^[0-9]{6}$`)

// Seller is the seller printed on GST invoices, as the console shows and edits it.
type Seller struct {
	Name    string `json:"name"`
	GSTIN   string `json:"gstin"`
	Address string `json:"address"`
	SAC     string `json:"sac"`
	// GSTRateBP is read only here: it comes from the deployment's settings.
	GSTRateBP int `json:"gst_rate_bp"`
	// Source is "console" once saved here, otherwise "settings" (the ECOGO_SELLER_* values).
	Source    string     `json:"source"`
	UpdatedAt *time.Time `json:"updated_at"`
	Invoicing bool       `json:"invoicing"`
}

func (s *Service) getSeller(w http.ResponseWriter, r *http.Request) error {
	var out Seller
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		row, err := q.GetSellerSettings(r.Context())
		switch {
		case db.IsNotFound(err):
			out = sellerView(s.seller, "settings", nil)
			return nil
		case err != nil:
			return err
		}
		sl, err := billing.LoadSeller(r.Context(), q, s.seller)
		if err != nil {
			return err
		}
		out = sellerView(sl, "console", &row.UpdatedAt)
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func sellerView(sl config.Seller, source string, at *time.Time) Seller {
	return Seller{Name: sl.Name, GSTIN: sl.GSTIN, Address: sl.Address, SAC: sl.SAC, GSTRateBP: sl.GSTRateBP,
		Source: source, UpdatedAt: at, Invoicing: sl.GSTIN != ""}
}

// saveSeller replaces the seller details. An empty GSTIN switches invoicing off. Invoices already
// issued keep the seller they were issued with.
func (s *Service) saveSeller(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name    string `json:"name"`
		GSTIN   string `json:"gstin"`
		Address string `json:"address"`
		SAC     string `json:"sac"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	req.GSTIN = strings.ToUpper(strings.TrimSpace(req.GSTIN))
	req.Address = strings.Join(strings.Fields(req.Address), " ")
	req.SAC = strings.TrimSpace(req.SAC)
	switch {
	case req.Name == "" || len(req.Name) > 200:
		return httpx.BadRequest("name", "Enter the company's legal name, up to 200 characters.")
	case req.GSTIN != "" && !config.ValidGSTIN(req.GSTIN):
		return httpx.BadRequest("gstin", "Enter a 15-character GSTIN such as 32ABCDE1234F1Z5, or leave it empty to switch invoicing off.")
	case req.GSTIN != "" && req.Address == "":
		return httpx.BadRequest("address", "Enter the registered address printed on invoices.")
	case len(req.Address) > 500:
		return httpx.BadRequest("address", "Keep the address under 500 characters.")
	case req.SAC != "" && !sacPattern.MatchString(req.SAC):
		return httpx.BadRequest("sac", "The SAC code is 6 digits.")
	}
	p, _ := auth.PrincipalFrom(r.Context())
	var out Seller
	err := s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		row, err := q.UpsertSellerSettings(r.Context(), dbq.UpsertSellerSettingsParams{
			Name: req.Name, Gstin: req.GSTIN, Address: req.Address, Sac: req.SAC, UpdatedBy: &p.UserID})
		if err != nil {
			return err
		}
		sl := s.seller
		sl.Name, sl.GSTIN, sl.Address, sl.SAC = row.Name, row.Gstin, row.Address, row.Sac
		out = sellerView(sl, "console", &row.UpdatedAt)
		return s.record(r, q, nil, "seller.save", "seller", "seller", nil)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
