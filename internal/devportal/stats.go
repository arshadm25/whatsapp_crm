package devportal

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// KeyUsage is one API key's calls in the last 24 hours.
type KeyUsage struct {
	APIKeyID uuid.UUID `json:"api_key_id"`
	Calls    int32     `json:"calls"`
	Errors   int32     `json:"errors"`
}

// EndpointStats is one webhook endpoint's deliveries in the last 24 hours.
type EndpointStats struct {
	EndpointID uuid.UUID `json:"endpoint_id"`
	Deliveries int32     `json:"deliveries"`
	Succeeded  int32     `json:"succeeded"`
	Failed     int32     `json:"failed"`  // dead: every retry failed
	Pending    int32     `json:"pending"` // not tried yet
}

// Stats is the Developers screen's last-24-hours summary. Rates are percentages, null when
// there is nothing to divide.
type Stats struct {
	APICalls          int32      `json:"api_calls"`
	APIErrors         int32      `json:"api_errors"`
	APIErrorRate      *float64   `json:"api_error_rate"`
	ByKey             []KeyUsage `json:"by_key"`
	WebhookDeliveries int32      `json:"webhook_deliveries"`
	WebhookSucceeded  int32      `json:"webhook_succeeded"`
	// WebhookSuccessRate leaves out deliveries not tried yet.
	WebhookSuccessRate *float64        `json:"webhook_success_rate"`
	ByEndpoint         []EndpointStats `json:"by_endpoint"`
}

func rate(part, whole int32) *float64 {
	if whole == 0 {
		return nil
	}
	v := float64(part) * 100 / float64(whole)
	return &v
}

func (s *Service) stats(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	since := time.Now().Add(-24 * time.Hour)
	out := Stats{ByKey: []KeyUsage{}, ByEndpoint: []EndpointStats{}}
	var tried int32
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		usage, err := q.APIUsageSince(r.Context(), since)
		if err != nil {
			return err
		}
		for _, u := range usage {
			out.APICalls += u.Calls
			out.APIErrors += u.Errors
			out.ByKey = append(out.ByKey, KeyUsage{APIKeyID: u.ApiKeyID, Calls: u.Calls, Errors: u.Errors})
		}
		deliveries, err := q.DeliveryStatsSince(r.Context(), since)
		if err != nil {
			return err
		}
		byEndpoint := map[uuid.UUID]*EndpointStats{}
		for _, d := range deliveries {
			e := byEndpoint[d.EndpointID]
			if e == nil {
				e = &EndpointStats{EndpointID: d.EndpointID}
				byEndpoint[d.EndpointID] = e
			}
			e.Deliveries += d.N
			switch d.Status {
			case dbq.DeliveryStatusSucceeded:
				e.Succeeded += d.N
			case dbq.DeliveryStatusDead:
				e.Failed += d.N
			case dbq.DeliveryStatusPending:
				e.Pending += d.N
			}
		}
		for _, e := range byEndpoint {
			out.ByEndpoint = append(out.ByEndpoint, *e)
			out.WebhookDeliveries += e.Deliveries
			out.WebhookSucceeded += e.Succeeded
			tried += e.Deliveries - e.Pending
		}
		return nil
	})
	if err != nil {
		return err
	}
	out.APIErrorRate = rate(out.APIErrors, out.APICalls)
	out.WebhookSuccessRate = rate(out.WebhookSucceeded, tried)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
