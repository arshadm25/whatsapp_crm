package metaclient

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
)

// FlowError is one problem Meta found in a Flow's JSON.
type FlowError struct {
	Error       string `json:"error"`
	ErrorType   string `json:"error_type"`
	Message     string `json:"message"`
	LineStart   int    `json:"line_start,omitempty"`
	LineEnd     int    `json:"line_end,omitempty"`
	ColumnStart int    `json:"column_start,omitempty"`
	ColumnEnd   int    `json:"column_end,omitempty"`
}

// CreatedFlow is Meta's answer to creating a Flow.
type CreatedFlow struct {
	ID               string      `json:"id"`
	Success          bool        `json:"success"`
	ValidationErrors []FlowError `json:"validation_errors"`
}

// FlowInfo is a Flow as Meta describes it.
type FlowInfo struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Status           string      `json:"status"` // DRAFT, PUBLISHED, DEPRECATED, BLOCKED, THROTTLED
	Categories       []string    `json:"categories"`
	ValidationErrors []FlowError `json:"validation_errors"`
	Preview          *struct {
		PreviewURL string `json:"preview_url"`
		ExpiresAt  string `json:"expires_at"`
	} `json:"preview"`
}

// CreateFlow creates an empty draft Flow on a WABA.
func (c *Client) CreateFlow(ctx context.Context, token, wabaID, name string, categories []string) (*CreatedFlow, error) {
	var out CreatedFlow
	body := map[string]any{"name": name, "categories": categories}
	if err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(wabaID)+"/flows", token, nil, body, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, fmt.Errorf("meta: flow create returned no id")
	}
	return &out, nil
}

// UploadFlowJSON replaces a draft Flow's JSON and returns what Meta's validator found.
func (c *Client) UploadFlowJSON(ctx context.Context, token, flowID string, flowJSON []byte) ([]FlowError, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "flow.json")
	_ = mw.WriteField("asset_type", "FLOW_JSON")
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="flow.json"`)
	h.Set("Content-Type", "application/json")
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(flowJSON); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	path := "/" + url.PathEscape(flowID) + "/assets"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+c.version+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out struct {
		Success          bool        `json:"success"`
		ValidationErrors []FlowError `json:"validation_errors"`
	}
	if err := c.send(ctx, req, http.MethodPost, path, token, &out); err != nil {
		return nil, err
	}
	return out.ValidationErrors, nil
}

// GetFlow reads a Flow's status, validation errors and a fresh preview link.
func (c *Client) GetFlow(ctx context.Context, token, flowID string) (*FlowInfo, error) {
	var out FlowInfo
	q := url.Values{"fields": {"id,name,status,categories,validation_errors,preview.invalidate(false)"}}
	if err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(flowID), token, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PublishFlow makes a draft Flow usable in messages. A published Flow can no longer be edited.
func (c *Client) PublishFlow(ctx context.Context, token, flowID string) error {
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(flowID)+"/publish", token, nil, nil, nil)
}

// DeprecateFlow retires a published Flow.
func (c *Client) DeprecateFlow(ctx context.Context, token, flowID string) error {
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(flowID)+"/deprecate", token, nil, nil, nil)
}

// DeleteFlow deletes a draft Flow.
func (c *Client) DeleteFlow(ctx context.Context, token, flowID string) error {
	return c.do(ctx, http.MethodDelete, "/"+url.PathEscape(flowID), token, nil, nil, nil)
}

// UpdateFlow changes a draft Flow's name or categories.
func (c *Client) UpdateFlow(ctx context.Context, token, flowID string, body map[string]any) error {
	return c.do(ctx, http.MethodPost, "/"+url.PathEscape(flowID), token, nil, body, nil)
}
