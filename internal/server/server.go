// Package server assembles the HTTP routers for the api and ingest deployments.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/arshadm25/whatsapp_crm/internal/admin"
	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/bots"
	"github.com/arshadm25/whatsapp_crm/internal/campaigns"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/deletion"
	"github.com/arshadm25/whatsapp_crm/internal/devportal"
	"github.com/arshadm25/whatsapp_crm/internal/flows"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/inbox"
	"github.com/arshadm25/whatsapp_crm/internal/media"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/metrics"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

type APIDeps struct {
	Config     *config.Config
	DB         *db.DB
	Log        *slog.Logger
	Auth       *auth.Service
	Onboarding *onboarding.Service
	Numbers    *numbers.Service
	Messaging  *messaging.Service
	Templates  *templates.Service
	Inbox      *inbox.Service
	Media      *media.Service
	Developers *devportal.Service
	Keys       *devportal.Authenticator
	Webhooks   *webhooks.Service
	Contacts   *contacts.Service
	Campaigns  *campaigns.Service
	Bots       *bots.Service
	Flows      *flows.Service
	Analytics  *analytics.Service
	Admin      *admin.Service
	Billing    *billing.Service
	Events     http.Handler
	Deletion   *deletion.Handler
	Metrics    *metrics.Metrics // optional
}

// NewAPI returns the api router:
//
//	/internal/...  dashboard-only endpoints (session cookie + CSRF)
//	/v1/...        public API: API keys (Authorization: Bearer), or the dashboard's session
func NewAPI(d APIDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, middleware.RealIP, httpx.Logger(d.Log), middleware.Recoverer)
	health(r, d.DB, d.Metrics)
	// Razorpay signs its deliveries; there is no session or CSRF token.
	r.Post("/webhooks/razorpay", d.Billing.Webhook)

	r.Route("/internal", func(r chi.Router) {
		r.Use(d.Auth.CSRF)
		r.Get("/config", publicConfig(d.Config))
		r.Route("/auth", d.Auth.Routes)
		r.Group(func(r chi.Router) {
			r.Use(d.Auth.RequireSession, auth.RequireTenant)
			r.Route("/onboarding", d.Onboarding.Routes)
			r.Route("/numbers", d.Numbers.InternalRoutes)
			r.Route("/templates", d.Templates.InternalRoutes)
			r.Route("/inbox", d.Inbox.InternalRoutes)
			r.Route("/developers", d.Developers.InternalRoutes)
			r.Route("/contacts", d.Contacts.InternalRoutes)
			r.Route("/campaigns", d.Campaigns.InternalRoutes)
			r.Route("/analytics", d.Analytics.InternalRoutes)
			r.Route("/team", d.Auth.TeamRoutes)
			r.Route("/billing", d.Billing.InternalRoutes)
			r.Method(http.MethodGet, "/events", d.Events)
		})
		// The admin console spans workspaces, so it needs a session but no tenant.
		r.Group(func(r chi.Router) {
			r.Use(d.Auth.RequireSession)
			r.Route("/admin", d.Admin.Routes)
		})
	})

	r.Route("/v1", func(r chi.Router) {
		// Signed download links work without a session.
		r.Get("/media/{id}/content", d.Media.Content())
		r.Get("/data-deletion/{code}", d.Deletion.StatusPage)
		r.Get("/openapi.yaml", devportal.OpenAPISpec)
		r.Get("/docs", devportal.Docs)
		r.Group(func(r chi.Router) {
			// API keys for integrations; the dashboard's session cookie and CSRF token otherwise.
			r.Use(d.Keys.Middleware(func(next http.Handler) http.Handler {
				return d.Auth.CSRF(d.Auth.RequireSession(auth.RequireTenant(next)))
			}))
			r.Route("/phone-numbers", d.Numbers.Routes)
			r.Route("/messages", d.Messaging.Routes)
			r.Route("/templates", d.Templates.Routes)
			r.Route("/conversations", d.Inbox.Routes)
			r.Route("/media", d.Media.Routes)
			r.Route("/webhook-endpoints", d.Webhooks.Routes)
			r.Route("/contacts", d.Contacts.Routes)
			r.Route("/campaigns", d.Campaigns.Routes)
			r.Route("/bots", d.Bots.Routes)
			r.Route("/flows", d.Flows.Routes)
			r.Route("/flow-submissions", d.Flows.SubmissionRoutes)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) { httpx.WriteError(w, r, d.Log, httpx.ErrNotFound) })
	return r
}

// NewIngest returns the Meta webhook receiver router: /meta takes Meta's handshake and
// deliveries, and /meta/data-deletion Meta's data deletion callback. It holds no tokens and no master key; it only checks signatures and enqueues.
func NewIngest(d *db.DB, meta http.Handler, del *deletion.Handler, m *metrics.Metrics, log *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, middleware.RealIP, httpx.Logger(log), middleware.Recoverer)
	health(r, d, m)
	r.Method(http.MethodGet, "/meta", meta)
	r.Method(http.MethodPost, "/meta", meta)
	r.Post("/meta/data-deletion", del.Callback)
	return r
}

// NewHealth returns a router with only the health endpoints, for the worker's probe port.
func NewHealth(d *db.DB, m *metrics.Metrics, log *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	health(r, d, m)
	return r
}

// health mounts the probes and, when m is set, /metrics and request metrics on every route
// mounted after this call.
func health(r chi.Router, d *db.DB, m *metrics.Metrics) {
	if m != nil {
		r.Use(m.Middleware)
		r.Method(http.MethodGet, "/metrics", m.Handler())
	}
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.Pool.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// publicConfig gives the dashboard what it needs to open Embedded Signup, so one dashboard
// build serves staging and production (each has its own Meta app).
func publicConfig(c *config.Config) http.HandlerFunc {
	body := map[string]any{
		"environment": c.Env,
		"meta": map[string]string{
			"app_id":            c.Meta.AppID,
			"config_id":         c.Meta.ConfigID,
			"graph_api_version": c.Meta.GraphAPIVersion,
		},
	}
	return func(w http.ResponseWriter, _ *http.Request) { httpx.JSON(w, http.StatusOK, body) }
}
