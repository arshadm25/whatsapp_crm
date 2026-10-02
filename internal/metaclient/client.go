// Package metaclient is the only code that talks to Meta's Graph API. Every Graph call goes
// through Client.do, so an API version bump or a new error mapping touches one package.
//
// Tokens are passed per call and never logged; errors are recorded (without tokens) through
// the optional ErrorRecorder for the platform console.
package metaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL   string // https://graph.facebook.com
	version   string // v24.0
	appID     string
	appSecret string
	http      *http.Client
	OnError   ErrorRecorder
}

// ErrorRecorder receives every failed Graph call (method, path without query, error).
type ErrorRecorder func(ctx context.Context, method, path string, err *Error)

func New(baseURL, version, appID, appSecret string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		version:   version,
		appID:     appID,
		appSecret: appSecret,
		http:      &http.Client{Timeout: 20 * time.Second},
	}
}

// Error is a Graph API error response.
type Error struct {
	HTTPStatus  int    `json:"-"`
	Message     string `json:"message"`
	Type        string `json:"type"`
	Code        int    `json:"code"`
	Subcode     int    `json:"error_subcode"`
	UserTitle   string `json:"error_user_title"`
	UserMessage string `json:"error_user_msg"`
	FBTraceID   string `json:"fbtrace_id"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("meta: %d (code %d, subcode %d): %s", e.HTTPStatus, e.Code, e.Subcode, e.Message)
}

// IsTokenInvalid reports an expired or revoked access token (Graph error 190).
func (e *Error) IsTokenInvalid() bool { return e.Code == 190 }

// Friendly returns the most useful text to show a client: Meta's user-facing message when present.
func (e *Error) Friendly() string {
	if e.UserMessage != "" {
		return e.UserMessage
	}
	return e.Message
}

// Retryable reports errors worth retrying later: throttling and Meta-side failures.
func (e *Error) Retryable() bool {
	switch e.Code {
	case 1, 2, 4, 17, 32, 80007, 130429, 131000, 131016:
		return true
	}
	return e.HTTPStatus >= 500
}

// do calls the Graph API. A non-nil body is sent as JSON; out receives the decoded response.
func (c *Client) do(ctx context.Context, method, path, token string, query url.Values, body, out any) error {
	u := c.baseURL + "/" + c.version + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(ctx, req, method, path, token, out)
}

// send runs a prepared Graph request and decodes the response or the Graph error.
func (c *Client) send(ctx context.Context, req *http.Request, method, path, token string, out any) error {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Do not wrap the *url.Error: its URL could carry a query secret (oauth exchange).
		return fmt.Errorf("meta: %s %s: request failed", method, path)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("meta: %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		var env struct {
			Error *Error `json:"error"`
		}
		metaErr := &Error{Message: strings.TrimSpace(string(raw))}
		if json.Unmarshal(raw, &env) == nil && env.Error != nil {
			metaErr = env.Error
		}
		metaErr.HTTPStatus = resp.StatusCode
		if c.OnError != nil {
			c.OnError(ctx, method, path, metaErr)
		}
		return metaErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("meta: %s %s: decode: %w", method, path, err)
	}
	return nil
}

// ExchangeCode swaps the Embedded Signup authorization code for a Business Integration
// System User access token. The code is short-lived, so this runs in the request that receives it.
func (c *Client) ExchangeCode(ctx context.Context, code string) (string, error) {
	q := url.Values{"client_id": {c.appID}, "client_secret": {c.appSecret}, "code": {code}}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.do(ctx, http.MethodGet, "/oauth/access_token", "", q, nil, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("meta: code exchange returned no token")
	}
	return out.AccessToken, nil
}

type WABA struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Currency   string `json:"currency"`
	TimezoneID string `json:"timezone_id"`
}

func (c *Client) GetWABA(ctx context.Context, token, wabaID string) (*WABA, error) {
	var out WABA
	q := url.Values{"fields": {"id,name,currency,timezone_id"}}
	if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(wabaID), token, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubscribeApp subscribes our app to the WABA's webhooks.
func (c *Client) SubscribeApp(ctx context.Context, token, wabaID string) error {
	var out struct {
		Success bool `json:"success"`
	}
	if err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(wabaID)+"/subscribed_apps", token, nil, nil, &out); err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("meta: subscribed_apps returned success=false")
	}
	return nil
}

type PhoneNumber struct {
	ID                     string `json:"id"`
	DisplayPhoneNumber     string `json:"display_phone_number"`
	VerifiedName           string `json:"verified_name"`
	NameStatus             string `json:"name_status"`
	QualityRating          string `json:"quality_rating"`
	MessagingLimitTier     string `json:"messaging_limit_tier"`
	CodeVerificationStatus string `json:"code_verification_status"`
	PlatformType           string `json:"platform_type"`
	IsOnBizApp             bool   `json:"is_on_biz_app"`
}

const phoneFields = "id,display_phone_number,verified_name,name_status,quality_rating,messaging_limit_tier,code_verification_status,platform_type,is_on_biz_app"

func (c *Client) ListPhoneNumbers(ctx context.Context, token, wabaID string) ([]PhoneNumber, error) {
	var out struct {
		Data []PhoneNumber `json:"data"`
	}
	q := url.Values{"fields": {phoneFields}}
	if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(wabaID)+"/phone_numbers", token, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) GetPhoneNumber(ctx context.Context, token, phoneNumberID string) (*PhoneNumber, error) {
	var out PhoneNumber
	q := url.Values{"fields": {phoneFields}}
	if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(phoneNumberID), token, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RegisterPhoneNumber registers a new number for Cloud API use, setting its two-step verification PIN.
// Not used for coexistence numbers, which stay registered to the WhatsApp Business app.
func (c *Client) RegisterPhoneNumber(ctx context.Context, token, phoneNumberID, pin string) error {
	body := map[string]string{"messaging_product": "whatsapp", "pin": pin}
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(phoneNumberID)+"/register", token, nil, body, nil)
}

// SMB app data sync types for coexistence onboarding; each must be requested within 24 hours.
const (
	SyncContacts = "smb_app_state_sync"
	SyncHistory  = "history"
)

// RequestSMBAppData asks Meta to send a coexistence number's contacts or chat history as webhooks.
func (c *Client) RequestSMBAppData(ctx context.Context, token, phoneNumberID, syncType string) error {
	body := map[string]string{"messaging_product": "whatsapp", "sync_type": syncType}
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(phoneNumberID)+"/smb_app_data", token, nil, body, nil)
}

type tenantKey struct{}

// WithTenant tags ctx with the tenant a Graph call is made for, so ErrorRecorder can attribute it.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenantID)
}

// TenantFrom returns the tenant set by WithTenant, or "".
func TenantFrom(ctx context.Context) string {
	s, _ := ctx.Value(tenantKey{}).(string)
	return s
}

// SendMessage sends one message from a phone number. body is the Cloud API message object
// without messaging_product, recipient_type and to, which are filled in here. It returns the
// wamid Meta assigned.
func (c *Client) SendMessage(ctx context.Context, token, phoneNumberID, to string, body map[string]any) (string, error) {
	req := make(map[string]any, len(body)+3)
	for k, v := range body {
		req[k] = v
	}
	req["messaging_product"] = "whatsapp"
	req["recipient_type"] = "individual"
	req["to"] = to
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(phoneNumberID)+"/messages", token, nil, req, &out); err != nil {
		return "", err
	}
	if len(out.Messages) == 0 || out.Messages[0].ID == "" {
		return "", fmt.Errorf("meta: send returned no message id")
	}
	return out.Messages[0].ID, nil
}

// MarkRead marks an inbound message (and earlier ones) as read, optionally showing a typing indicator.
func (c *Client) MarkRead(ctx context.Context, token, phoneNumberID, wamid string, typing bool) error {
	body := map[string]any{"messaging_product": "whatsapp", "status": "read", "message_id": wamid}
	if typing {
		body["typing_indicator"] = map[string]string{"type": "text"}
	}
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(phoneNumberID)+"/messages", token, nil, body, nil)
}

// Template is a message template as Meta lists it.
type Template struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Language        string          `json:"language"`
	Status          string          `json:"status"`
	Category        string          `json:"category"`
	ParameterFormat string          `json:"parameter_format"`
	RejectedReason  string          `json:"rejected_reason"`
	Components      json.RawMessage `json:"components"`
	QualityScore    *struct {
		Score string `json:"score"`
	} `json:"quality_score"`
}

const templateFields = "id,name,language,status,category,parameter_format,rejected_reason,components,quality_score"

// ListTemplates returns every template on a WABA, following Meta's paging.
func (c *Client) ListTemplates(ctx context.Context, token, wabaID string) ([]Template, error) {
	var all []Template
	after := ""
	for range 50 { // 50 pages of 100 is far above Meta's per-WABA template limit
		q := url.Values{"fields": {templateFields}, "limit": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var out struct {
			Data   []Template `json:"data"`
			Paging struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(wabaID)+"/message_templates", token, q, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.Paging.Next == "" || out.Paging.Cursors.After == "" {
			return all, nil
		}
		after = out.Paging.Cursors.After
	}
	return all, nil
}

// CreatedTemplate is Meta's answer to a template submission.
type CreatedTemplate struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Category string `json:"category"`
}

// CreateTemplate submits a new template for review. body holds name, language, category,
// components and optionally parameter_format, in Meta's shape.
func (c *Client) CreateTemplate(ctx context.Context, token, wabaID string, body map[string]any) (*CreatedTemplate, error) {
	var out CreatedTemplate
	if err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(wabaID)+"/message_templates", token, nil, body, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, fmt.Errorf("meta: template create returned no id")
	}
	return &out, nil
}

// EditTemplate changes an existing template's category or components and resubmits it.
func (c *Client) EditTemplate(ctx context.Context, token, templateID string, body map[string]any) error {
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(templateID), token, nil, body, nil)
}

// DeleteTemplate deletes every language of a template name on a WABA.
func (c *Client) DeleteTemplate(ctx context.Context, token, wabaID, name string) error {
	q := url.Values{"name": {name}}
	return c.do(ctx, http.MethodDelete, "/"+url.PathEscape(wabaID)+"/message_templates", token, q, nil, nil)
}
