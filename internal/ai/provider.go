// Package ai answers customers from a workspace's knowledge base (BRD Phase 2). A workspace adds
// FAQs, text, documents and web pages; the chatbot's AI step finds the passages that match a
// customer's question, asks a language model to answer from them alone, and hands the
// conversation to a person when the model is not confident enough. Each plan includes a number
// of AI replies per billing month.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arshadm25/whatsapp_crm/internal/config"
)

// Message is one turn sent to the model.
type Message struct {
	Role    string `json:"role"` // user or assistant
	Content string `json:"content"`
}

type Request struct {
	System    string
	Messages  []Message
	MaxTokens int
}

type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// Provider is a language model service. Anthropic is the only one built in; another provider
// only has to implement this.
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// ErrNotConfigured is returned when the deployment has no AI provider key.
var ErrNotConfigured = errors.New("ai: no provider configured")

type unconfigured struct{}

func (unconfigured) Complete(context.Context, Request) (Response, error) {
	return Response{}, ErrNotConfigured
}

// Configured reports whether p can answer.
func Configured(p Provider) bool {
	_, off := p.(unconfigured)
	return p != nil && !off
}

// New returns the provider named in the configuration, or one that always answers
// ErrNotConfigured when there is no API key.
func New(cfg config.AI, client *http.Client) Provider {
	if cfg.APIKey == "" || cfg.Provider != "anthropic" {
		return unconfigured{}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Anthropic{key: cfg.APIKey, model: cfg.Model, baseURL: strings.TrimRight(cfg.BaseURL, "/"), http: client}
}

// Anthropic calls the Messages API.
type Anthropic struct {
	key, model, baseURL string
	http                *http.Client
}

func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	if req.MaxTokens <= 0 {
		req.MaxTokens = 600
	}
	body, err := json.Marshal(map[string]any{
		"model": a.model, "max_tokens": req.MaxTokens, "system": req.System, "messages": req.Messages,
	})
	if err != nil {
		return Response{}, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	r.Header.Set("x-api-key", a.key)
	r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Set("content-type", "application/json")
	resp, err := a.http.Do(r)
	if err != nil {
		return Response{}, fmt.Errorf("ai: request failed") // the error could carry the URL, not the key
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode >= 400 {
		return Response{}, fmt.Errorf("ai: provider answered %d", resp.StatusCode)
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("ai: unreadable provider response")
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return Response{Text: text.String(), InputTokens: out.Usage.Input, OutputTokens: out.Usage.Output}, nil
}
