package numbers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/credentials"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
)

// tierLimits is how many customers Meta lets a number start conversations with in 24 hours.
var tierLimits = map[string]int32{
	"TIER_250": 250, "TIER_1K": 1000, "TIER_2K": 2000, "TIER_10K": 10000, "TIER_100K": 100000,
}

// DailyLimit returns a number's messaging limit; -1 means unlimited. A number Meta has not
// reported a tier for yet gets the starting limit.
func DailyLimit(tier *string) int32 {
	if tier == nil {
		return 250
	}
	t := strings.ToUpper(*tier)
	if t == "TIER_UNLIMITED" {
		return -1
	}
	if n, ok := tierLimits[t]; ok {
		return n
	}
	return 250
}

var qualityRank = map[string]int{"red": 1, "yellow": 2, "green": 3}

// qualityDropped is true when the rating fell (green to yellow, yellow to red) in the last week.
func qualityDropped(p dbq.PhoneNumber, now time.Time) bool {
	if p.PreviousQualityRating == nil || p.QualityChangedAt == nil {
		return false
	}
	if now.Sub(*p.QualityChangedAt) > 7*24*time.Hour {
		return false
	}
	prev, cur := qualityRank[string(*p.PreviousQualityRating)], qualityRank[string(p.QualityRating)]
	return prev > 0 && cur > 0 && cur < prev
}

func quality(s string) dbq.QualityRating {
	q := dbq.QualityRating(strings.ToLower(s))
	if q.Valid() {
		return q
	}
	return dbq.QualityRatingUnknown
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// sync refreshes every connected number of the workspace from Meta: name and its review,
// quality rating and messaging limit.
func (s *Service) sync(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	ctx := metaclient.WithTenant(r.Context(), p.TenantID.String())
	type account struct {
		waba, token string
	}
	var accts []account
	err := s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListWhatsAppAccounts(ctx)
		if err != nil {
			return err
		}
		for _, a := range rows {
			if a.Status != dbq.ConnectionStatusConnected {
				continue
			}
			token, err := credentials.Token(ctx, q, s.keys, a)
			if err != nil {
				return err
			}
			accts = append(accts, account{a.WabaID, token})
		}
		return nil
	})
	if err != nil {
		return err
	}
	synced := 0
	for _, a := range accts {
		nums, err := s.meta.ListPhoneNumbers(ctx, a.token, a.waba)
		if err != nil {
			return metaError(err)
		}
		err = s.db.InTenant(ctx, p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
			for _, n := range nums {
				display := n.DisplayPhoneNumber
				if display == "" {
					display = n.ID
				}
				_, err := q.UpdatePhoneNumberFromMeta(ctx, dbq.UpdatePhoneNumberFromMetaParams{
					PhoneNumberID: n.ID, DisplayPhoneNumber: display, VerifiedName: nonEmpty(n.VerifiedName),
					NameStatus: nonEmpty(n.NameStatus), QualityRating: quality(n.QualityRating),
					MessagingLimitTier: nonEmpty(n.MessagingLimitTier), CodeVerificationStatus: nonEmpty(n.CodeVerificationStatus),
				})
				if db.IsNotFound(err) {
					continue // a number on the account that was not connected here
				}
				if err != nil {
					return err
				}
				synced++
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"synced": synced})
	return nil
}

// metaError turns a failed Graph call into an API error.
func metaError(err error) error {
	var me *metaclient.Error
	if !errors.As(err, &me) {
		return err
	}
	if me.IsTokenInvalid() {
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: "number_not_connected", MetaErrorCode: me.Code,
			Message: "Meta no longer accepts this account's access. Reconnect WhatsApp from Numbers."}
	}
	return &httpx.Error{Status: http.StatusBadGateway, Code: "meta_error", Message: me.Friendly(), MetaErrorCode: me.Code}
}
