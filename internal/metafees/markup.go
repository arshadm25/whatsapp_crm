package metafees

import (
	"context"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// Markup returns Ecogo's markup on Meta's fees for one workspace, in basis points: the
// workspace's own rate when staff set one, otherwise the platform rate saved in Admin > Settings,
// otherwise base (ECOGO_META_MARKUP_BP).
func Markup(ctx context.Context, q *dbq.Queries, own *int32, base int) (int, error) {
	if own != nil {
		return int(*own), nil
	}
	ps, err := q.GetPlatformSettings(ctx)
	if db.IsNotFound(err) {
		return base, nil
	}
	if err != nil {
		return 0, err
	}
	if ps.MetaMarkupBp != nil {
		return int(*ps.MetaMarkupBp), nil
	}
	return base, nil
}
