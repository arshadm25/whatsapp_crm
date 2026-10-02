package metaclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PricingPoint is Meta's count and cost for one day, pricing category and country.
type PricingPoint struct {
	Start    time.Time
	Volume   int64
	Cost     float64 // in the WABA's currency
	Category string  // lower case: marketing, utility, authentication, service
	Country  string
}

// PricingAnalytics reads what Meta billed a WABA between two moments, one point per day, category
// and country. It is the data the daily reconciliation compares our usage with.
func (c *Client) PricingAnalytics(ctx context.Context, token, wabaID string, start, end time.Time) ([]PricingPoint, error) {
	fields := fmt.Sprintf(`pricing_analytics.start(%d).end(%d).granularity(DAILY).dimensions(["PRICING_CATEGORY","COUNTRY"])`, start.Unix(), end.Unix())
	var out struct {
		PricingAnalytics struct {
			Data []struct {
				DataPoints []struct {
					Start    int64   `json:"start"`
					Volume   int64   `json:"volume"`
					Cost     float64 `json:"cost"`
					Category string  `json:"pricing_category"`
					Country  string  `json:"country"`
				} `json:"data_points"`
			} `json:"data"`
		} `json:"pricing_analytics"`
	}
	if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(wabaID), token, url.Values{"fields": {fields}}, nil, &out); err != nil {
		return nil, err
	}
	var pts []PricingPoint
	for _, d := range out.PricingAnalytics.Data {
		for _, p := range d.DataPoints {
			pts = append(pts, PricingPoint{Start: time.Unix(p.Start, 0).UTC(), Volume: p.Volume, Cost: p.Cost,
				Category: normaliseCategory(p.Category), Country: strings.ToUpper(p.Country)})
		}
	}
	return pts, nil
}

// normaliseCategory maps Meta's pricing categories (MARKETING, UTILITY, AUTHENTICATION,
// AUTHENTICATION_INTERNATIONAL, SERVICE, MARKETING_LITE) to the names used in usage records.
func normaliseCategory(s string) string {
	s = strings.ToLower(s)
	for _, k := range []string{"marketing", "authentication", "utility", "service"} {
		if strings.Contains(s, k) {
			return k
		}
	}
	return s
}

// AttachCreditLine shares Ecogo's credit line with a client's WABA, so Meta bills Ecogo for the
// WABA's messages. token is the partner's own (not the client's), and currency the WABA's.
// It returns the allocation ID.
func (c *Client) AttachCreditLine(ctx context.Context, token, creditLineID, wabaID, currency string) (string, error) {
	var out struct {
		AllocationConfigID string `json:"allocation_config_id"`
		WabaID             string `json:"waba_id"`
	}
	q := url.Values{"waba_id": {wabaID}, "waba_currency": {currency}}
	if err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(creditLineID)+"/whatsapp_credit_sharing_and_attach", token, q, nil, &out); err != nil {
		return "", err
	}
	if out.AllocationConfigID == "" {
		return "", fmt.Errorf("meta: credit line attach returned no allocation id")
	}
	return out.AllocationConfigID, nil
}
