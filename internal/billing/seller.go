package billing

import (
	"context"

	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// LoadSeller returns the seller printed on invoices: the details saved in the admin console when
// there are any, otherwise base (the ECOGO_SELLER_* settings). The GST rate is the one saved in
// Admin > Settings when set, otherwise base's. Nothing is invoiced while the returned GSTIN is empty.
func LoadSeller(ctx context.Context, q *dbq.Queries, base config.Seller) (config.Seller, error) {
	out := base
	ps, err := q.GetPlatformSettings(ctx)
	switch {
	case err == nil:
		if ps.GstRateBp != nil {
			out.GSTRateBP = int(*ps.GstRateBp)
		}
	case !db.IsNotFound(err):
		return config.Seller{}, err
	}
	row, err := q.GetSellerSettings(ctx)
	if db.IsNotFound(err) {
		return out, nil
	}
	if err != nil {
		return config.Seller{}, err
	}
	out.Name, out.GSTIN, out.Address, out.SAC = row.Name, row.Gstin, row.Address, row.Sac
	return out, nil
}
