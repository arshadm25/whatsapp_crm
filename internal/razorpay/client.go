// Package razorpay is the only code that calls Razorpay: customers and subscriptions for our
// own plans (D9), and the webhook signature check. Amounts are in paise.
package razorpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL   string // https://api.razorpay.com
	keyID     string
	keySecret string
	http      *http.Client
}

func New(baseURL, keyID, keySecret string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), keyID: keyID, keySecret: keySecret,
		http: &http.Client{Timeout: 20 * time.Second}}
}

// Configured reports whether API keys are set; without them billing shows plans but cannot
// take payment.
func (c *Client) Configured() bool { return c != nil && c.keyID != "" && c.keySecret != "" }

// Error is a Razorpay API error response.
type Error struct {
	HTTPStatus  int    `json:"-"`
	Code        string `json:"code"`
	Description string `json:"description"`
	Field       string `json:"field"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("razorpay: %d %s: %s", e.HTTPStatus, e.Code, e.Description)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var env struct {
			Error Error `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		env.Error.HTTPStatus = resp.StatusCode
		return &env.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

type Customer struct {
	ID string `json:"id"`
}

// CreateCustomer returns the customer with this email, creating it if needed.
func (c *Client) CreateCustomer(ctx context.Context, name, email string, notes map[string]string) (Customer, error) {
	var out Customer
	err := c.do(ctx, http.MethodPost, "/v1/customers", map[string]any{
		"name": name, "email": email, "fail_existing": "0", "notes": notes,
	}, &out)
	return out, err
}

// Subscription is the part of Razorpay's subscription entity we use.
type Subscription struct {
	ID           string            `json:"id"`
	PlanID       string            `json:"plan_id"`
	CustomerID   string            `json:"customer_id"`
	Status       string            `json:"status"` // created, authenticated, active, pending, halted, cancelled, completed, expired, paused
	CurrentStart *int64            `json:"current_start"`
	CurrentEnd   *int64            `json:"current_end"`
	EndedAt      *int64            `json:"ended_at"`
	Quantity     int               `json:"quantity"`
	ShortURL     string            `json:"short_url"`
	Notes        map[string]string `json:"notes"`
}

type NewSubscription struct {
	PlanID     string
	CustomerID string
	// TotalCount is how many monthly cycles Razorpay may charge; it is required.
	TotalCount int
	// StartAt delays the first charge, so a trial's remaining days stay free.
	StartAt *time.Time
	Notes   map[string]string
	// Quantity multiplies the plan's amount; zero means one.
	Quantity int
}

func (c *Client) CreateSubscription(ctx context.Context, s NewSubscription) (Subscription, error) {
	body := map[string]any{
		"plan_id": s.PlanID, "customer_id": s.CustomerID, "total_count": s.TotalCount,
		"customer_notify": 1, "notes": s.Notes,
	}
	if s.Quantity > 1 {
		body["quantity"] = s.Quantity
	}
	if s.StartAt != nil {
		body["start_at"] = s.StartAt.Unix()
	}
	var out Subscription
	err := c.do(ctx, http.MethodPost, "/v1/subscriptions", body, &out)
	return out, err
}

// ChangePlan moves an active subscription to another plan from its next cycle.
func (c *Client) ChangePlan(ctx context.Context, subscriptionID, planID string) (Subscription, error) {
	var out Subscription
	err := c.do(ctx, http.MethodPatch, "/v1/subscriptions/"+subscriptionID, map[string]any{
		"plan_id": planID, "schedule_change_at": "cycle_end", "customer_notify": 1,
	}, &out)
	return out, err
}

// ChangeQuantity changes how many units a subscription charges for, immediately (Razorpay
// charges the prorated difference) or from the next cycle.
func (c *Client) ChangeQuantity(ctx context.Context, subscriptionID string, quantity int, now bool) (Subscription, error) {
	when := "cycle_end"
	if now {
		when = "now"
	}
	var out Subscription
	err := c.do(ctx, http.MethodPatch, "/v1/subscriptions/"+subscriptionID, map[string]any{
		"quantity": quantity, "schedule_change_at": when, "customer_notify": 1,
	}, &out)
	return out, err
}

// Cancel stops a subscription, at the end of the paid cycle when atCycleEnd is set.
func (c *Client) Cancel(ctx context.Context, subscriptionID string, atCycleEnd bool) (Subscription, error) {
	flag := 0
	if atCycleEnd {
		flag = 1
	}
	var out Subscription
	err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+subscriptionID+"/cancel", map[string]any{"cancel_at_cycle_end": flag}, &out)
	return out, err
}

// VerifySignature checks X-Razorpay-Signature: hex HMAC-SHA256 of the raw body with the
// webhook secret.
func VerifySignature(body []byte, signature, secret string) bool {
	if secret == "" || signature == "" {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	want := hex.EncodeToString(m.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signature))
}

// Event is a webhook delivery. Only subscription events carry what billing needs.
type Event struct {
	Event   string `json:"event"`
	Payload struct {
		Subscription *struct {
			Entity Subscription `json:"entity"`
		} `json:"subscription"`
	} `json:"payload"`
}
