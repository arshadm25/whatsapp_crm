// Package server assembles the HTTP routers for the api and ingest deployments.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
)

type APIDeps struct {
	Config     *config.Config
	DB         *db.DB
	Log        *slog.Logger
	Auth       *auth.Service
	Onboarding *onboarding.Service
	Numbers    *numbers.Service
}

// NewAPI returns the api router:
//
//	/internal/...  dashboard-only endpoints (session cookie + CSRF)
//	/v1/...        public API; session auth for the dashboard today, API keys arrive with D8
func NewAPI(d APIDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, middleware.RealIP, httpx.Logger(d.Log), middleware.Recoverer)
	health(r, d.DB)

	r.Route("/internal", func(r chi.Router) {
		r.Use(d.Auth.CSRF)
		r.Get("/config", publicConfig(d.Config))
		r.Route("/auth", d.Auth.Routes)
		r.Group(func(r chi.Router) {
			r.Use(d.Auth.RequireSession, auth.RequireTenant)
			r.Route("/onboarding", d.Onboarding.Routes)
		})
	})

	r.Route("/v1", func(r chi.Router) {
		r.Use(d.Auth.CSRF, d.Auth.RequireSession, auth.RequireTenant)
		r.Route("/phone-numbers", d.Numbers.Routes)
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) { httpx.WriteError(w, r, d.Log, httpx.ErrNotFound) })
	return r
}

// NewIngest returns the Meta webhook receiver router. The receiver itself (signature check,
// enqueue, 200) is the next release 1 slice; this deployment already exists so ingress,
// TLS and the Meta app's callback URL can be set up ahead of it.
func NewIngest(d *db.DB, log *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID, middleware.RealIP, httpx.Logger(log), middleware.Recoverer)
	health(r, d)
	return r
}

func health(r chi.Router, d *db.DB) {
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
