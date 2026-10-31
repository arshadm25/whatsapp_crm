package billing

import (
	"context"

	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// LoadSeller returns the seller printed on invoices: the details saved in the admin console when
// there are any, otherwise base (the ECOGO_SELLER_* settings). The GST rate always comes from
// base. Nothing is invoiced while the returned GSTIN is empty.
func LoadSeller(ctx context.Context, q *dbq.Queries, base config.Seller) (config.Seller, error) {
	row, err := q.GetSellerSettings(ctx)
	if db.IsNotFound(err) {
		return base, nil
	}
	if err != nil {
		return config.Seller{}, err
	}
	out := base
	out.Name, out.GSTIN, out.Address, out.SAC = row.Name, row.Gstin, row.Address, row.Sac
	return out, nil
}
