// Command ecogo is the single binary behind every backend deployment:
//
//	ecogo api      public /v1 API and dashboard endpoints
//	ecogo ingest   Meta webhook receiver
//	ecogo worker   River job workers
//	ecogo migrate  apply database migrations (Helm pre-upgrade Job)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/arshadm25/whatsapp_crm/internal/admin"
	"github.com/arshadm25/whatsapp_crm/internal/analytics"
	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/billing"
	"github.com/arshadm25/whatsapp_crm/internal/campaigns"
	"github.com/arshadm25/whatsapp_crm/internal/config"
	"github.com/arshadm25/whatsapp_crm/internal/contacts"
	"github.com/arshadm25/whatsapp_crm/internal/crypto/envelope"
	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/devportal"
	"github.com/arshadm25/whatsapp_crm/internal/events"
	"github.com/arshadm25/whatsapp_crm/internal/inbox"
	"github.com/arshadm25/whatsapp_crm/internal/jobs"
	"github.com/arshadm25/whatsapp_crm/internal/mailer"
	"github.com/arshadm25/whatsapp_crm/internal/media"
	"github.com/arshadm25/whatsapp_crm/internal/messaging"
	"github.com/arshadm25/whatsapp_crm/internal/metaclient"
	"github.com/arshadm25/whatsapp_crm/internal/metaevents"
	"github.com/arshadm25/whatsapp_crm/internal/numbers"
	"github.com/arshadm25/whatsapp_crm/internal/onboarding"
	"github.com/arshadm25/whatsapp_crm/internal/razorpay"
	"github.com/arshadm25/whatsapp_crm/internal/server"
	"github.com/arshadm25/whatsapp_crm/internal/storage"
	"github.com/arshadm25/whatsapp_crm/internal/templates"
	"github.com/arshadm25/whatsapp_crm/internal/webhooks"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ecogo api|ingest|worker|migrate|grant-admin <email>|revoke-admin <email>")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := newLogger(cfg.LogLevel).With("service", os.Args[1])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "api":
		err = runAPI(ctx, cfg, log)
	case "ingest":
		err = runIngest(ctx, cfg, log)
	case "worker":
		err = runWorker(ctx, cfg, log)
	case "migrate":
		if err = cfg.Require("ECOGO_MIGRATION_DATABASE_URL"); err == nil {
			err = db.Migrate(ctx, cfg.MigrationDatabaseURL, log)
		}
	case "grant-admin", "revoke-admin":
		err = setPlatformAdmin(ctx, cfg, os.Args[1:], log)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func runAPI(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if err := cfg.Require("ECOGO_DATABASE_URL", "ECOGO_MASTER_KEYS", "ECOGO_APP_SECRET",
		"ECOGO_META_APP_ID", "ECOGO_META_APP_SECRET", "ECOGO_META_CONFIG_ID"); err != nil {
		return err
	}
	d, keys, meta, err := common(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer d.Close()
	rc, err := jobs.NewInsertOnly(d.Pool, log)
	if err != nil {
		return err
	}
	store, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	hub := events.NewHub(d.Pool, log)
	go hub.Run(ctx)
	h := server.NewAPI(server.APIDeps{
		Config:     cfg,
		DB:         d,
		Log:        log,
		Auth:       auth.NewService(d, keys, cfg, mailer.NewSMTP(cfg.Mail), log),
		Onboarding: onboarding.NewService(d, keys, meta, rc, log),
		Numbers:    numbers.NewService(d, keys, meta, log),
		Messaging:  messaging.NewService(d, keys, meta, rc, log),
		Templates:  templates.NewService(d, keys, meta, log),
		Inbox:      inbox.NewService(d, log),
		Media:      media.NewService(d, store, media.NewSigner(cfg.AppSecret), log),
		Developers: devportal.NewService(d, log),
		Keys:       devportal.NewAuthenticator(d, devportal.NewLimiter(60), log),
		Webhooks:   webhooks.NewService(d, keys, rc, log),
		Contacts:   contacts.NewService(d, log),
		Campaigns:  campaigns.NewService(d, rc, log),
		Analytics:  analytics.NewService(d, log),
		Admin:      admin.NewService(d, log),
		Billing: billing.NewService(d, razorpay.New(cfg.Razorpay.BaseURL, cfg.Razorpay.KeyID, cfg.Razorpay.KeySecret),
			cfg.Razorpay.WebhookSecret, cfg.Seller, rc, log),
		Events: hub,
	})
	return serve(ctx, cfg.HTTPAddr, h, log)
}

func runIngest(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if err := cfg.Require("ECOGO_DATABASE_URL", "ECOGO_META_APP_SECRET", "ECOGO_META_WEBHOOK_VERIFY_TOKEN"); err != nil {
		return err
	}
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer d.Close()
	rc, err := jobs.NewInsertOnly(d.Pool, log)
	if err != nil {
		return err
	}
	hooks := metaevents.NewHandler(cfg.Meta.AppSecret, cfg.Meta.VerifyToken, rc, log)
	return serve(ctx, cfg.HTTPAddr, server.NewIngest(d, hooks, log), log)
}

func runWorker(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	if err := cfg.Require("ECOGO_DATABASE_URL", "ECOGO_MASTER_KEYS", "ECOGO_META_APP_ID", "ECOGO_META_APP_SECRET"); err != nil {
		return err
	}
	d, keys, meta, err := common(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer d.Close()

	store, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, onboarding.NewWorker(d, keys, meta, templates.NewSyncer(d, meta), log))
	river.AddWorker(workers, metaevents.NewProcessor(d, log))
	river.AddWorker(workers, messaging.NewWorker(d, keys, meta, media.NewUploader(store, meta), log))
	river.AddWorker(workers, media.NewDownloadWorker(d, keys, meta, store, log))
	river.AddWorker(workers, webhooks.NewWorker(d, keys, nil, log))
	river.AddWorker(workers, campaigns.NewWorker(d, log))
	river.AddWorker(workers, analytics.NewWorker(d, log))
	river.AddWorker(workers, billing.NewEmailWorker(d, mailer.NewSMTP(cfg.Mail), cfg.PublicAppURL, log))
	rc, err := jobs.NewWorkerClient(d.Pool, workers, []*river.PeriodicJob{analytics.Periodic()}, log)
	if err != nil {
		return err
	}
	if err := rc.Start(ctx); err != nil {
		return err
	}
	log.Info("worker started")

	// The worker has no traffic port, but Kubernetes probes and metrics need one.
	go func() {
		if err := serve(ctx, cfg.HTTPAddr, server.NewHealth(d, log), log); err != nil {
			log.Error("worker health server", "err", err)
		}
	}()
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return rc.Stop(stopCtx)
}

// setPlatformAdmin gives or takes away access to the admin console (A1). The user signs in
// as usual and must turn on two-step verification before the console opens.
func setPlatformAdmin(ctx context.Context, cfg *config.Config, args []string, log *slog.Logger) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: ecogo %s <email>", args[0])
	}
	if err := cfg.Require("ECOGO_DATABASE_URL"); err != nil {
		return err
	}
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer d.Close()
	email := strings.ToLower(strings.TrimSpace(args[1]))
	grant := args[0] == "grant-admin"
	return d.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
		n, err := q.SetPlatformAdmin(ctx, dbq.SetPlatformAdminParams{Email: email, IsPlatformAdmin: grant})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no user with email %s; they need to sign up first", email)
		}
		log.Info("platform admin updated", "email", email, "platform_admin", grant)
		return nil
	})
}

// common opens the database and builds the keyring and Meta client.
func common(ctx context.Context, cfg *config.Config, log *slog.Logger) (*db.DB, *envelope.Keyring, *metaclient.Client, error) {
	keys, err := envelope.NewKeyring(cfg.MasterKeys, cfg.MasterKeyVersion)
	if err != nil {
		return nil, nil, nil, err
	}
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	meta := metaclient.New(cfg.Meta.GraphBaseURL, cfg.Meta.GraphAPIVersion, cfg.Meta.AppID, cfg.Meta.AppSecret)
	meta.OnError = recordMetaError(d, log)
	return d, keys, meta, nil
}

// recordMetaError writes failed Graph calls to meta_api_errors for the platform console.
func recordMetaError(d *db.DB, log *slog.Logger) metaclient.ErrorRecorder {
	return func(ctx context.Context, method, path string, e *metaclient.Error) {
		log.Warn("meta api error", "method", method, "path", path, "status", e.HTTPStatus,
			"code", e.Code, "subcode", e.Subcode, "fbtrace_id", e.FBTraceID)
		ctx = context.WithoutCancel(ctx)
		err := d.Global(ctx, func(q *dbq.Queries, _ pgx.Tx) error {
			p := dbq.InsertMetaAPIErrorParams{
				Method: method, Path: path, HttpStatus: int32(e.HTTPStatus),
				Message: &e.Message, FbtraceID: &e.FBTraceID,
			}
			if t, err := parseUUID(metaclient.TenantFrom(ctx)); err == nil {
				p.TenantID = &t
			}
			code, sub := int32(e.Code), int32(e.Subcode)
			p.Code, p.Subcode = &code, &sub
			return q.InsertMetaAPIError(ctx, p)
		})
		if err != nil {
			log.Error("record meta api error", "err", err)
		}
	}
}

func serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

func parseUUID(s string) (uuid.UUID, error) { return uuid.Parse(s) }
