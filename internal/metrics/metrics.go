// Package metrics serves Prometheus metrics at /metrics on every Ecogo process: HTTP traffic,
// the database pool, River job queues and Meta webhook health. The endpoint is not routed by
// the ingress, so it is reachable only inside the cluster.
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/arshadm25/whatsapp_crm/internal/db"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// New builds a registry with the Go runtime, process, HTTP, database pool and queue metrics.
func New(d *db.DB) *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ecogo_http_requests_total", Help: "HTTP requests by method, route pattern and status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "ecogo_http_request_duration_seconds", Help: "HTTP request duration by route pattern.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
	}
	m.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests, m.duration)
	if d != nil {
		m.reg.MustRegister(newPoolCollector(d), newQueueCollector(d))
	}
	return m
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Middleware counts and times requests. It labels by the matched route pattern, never the raw
// path, so IDs in URLs do not create a series each; /metrics and the probes are left out.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		switch route {
		case "/metrics", "/healthz", "/readyz":
			return
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// poolCollector reports the pgx connection pool.
type poolCollector struct {
	db                                                 *db.DB
	acquired, idle, total, max, waitCount, waitSeconds *prometheus.Desc
}

func newPoolCollector(d *db.DB) *poolCollector {
	g := func(name, help string) *prometheus.Desc { return prometheus.NewDesc(name, help, nil, nil) }
	return &poolCollector{
		db:          d,
		acquired:    g("ecogo_db_pool_acquired_connections", "Connections in use."),
		idle:        g("ecogo_db_pool_idle_connections", "Idle connections."),
		total:       g("ecogo_db_pool_total_connections", "Open connections."),
		max:         g("ecogo_db_pool_max_connections", "Pool size limit."),
		waitCount:   g("ecogo_db_pool_acquire_waits_total", "Acquires that had to wait for a free connection."),
		waitSeconds: g("ecogo_db_pool_acquire_wait_seconds_total", "Time spent waiting for a free connection."),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.waitCount, c.waitSeconds} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.db.Pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) { ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v) }
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(c.acquired, float64(s.AcquiredConns()))
	gauge(c.idle, float64(s.IdleConns()))
	gauge(c.total, float64(s.TotalConns()))
	gauge(c.max, float64(s.MaxConns()))
	counter(c.waitCount, float64(s.EmptyAcquireCount()))
	counter(c.waitSeconds, s.AcquireDuration().Seconds())
}

// queueCollector reads job queue and Meta webhook state from the database on scrape, cached
// briefly so several scrapers cannot load it.
type queueCollector struct {
	db *db.DB

	jobs, oldest, webhookBacklog, webhookErrors, metaErrors *prometheus.Desc

	mu      sync.Mutex
	fetched time.Time
	snap    snapshot
}

type snapshot struct {
	jobs           []jobRow
	webhookBacklog float64 // received more than 2 minutes ago and still unprocessed
	webhookErrors  float64 // failed in the last hour
	metaErrors     float64 // Graph API errors in the last hour
	ok             bool
}

type jobRow struct {
	queue, state string
	count        float64
	oldestSecs   float64
}

const cacheFor = 15 * time.Second

func newQueueCollector(d *db.DB) *queueCollector {
	return &queueCollector{
		db:             d,
		jobs:           prometheus.NewDesc("ecogo_jobs", "Background jobs by queue and state.", []string{"queue", "state"}, nil),
		oldest:         prometheus.NewDesc("ecogo_jobs_oldest_seconds", "Age of the oldest job waiting to run, by queue.", []string{"queue"}, nil),
		webhookBacklog: prometheus.NewDesc("ecogo_meta_webhook_backlog", "Meta webhook deliveries unprocessed for over 2 minutes.", nil, nil),
		webhookErrors:  prometheus.NewDesc("ecogo_meta_webhook_errors_1h", "Meta webhook deliveries that failed processing in the last hour.", nil, nil),
		metaErrors:     prometheus.NewDesc("ecogo_meta_api_errors_1h", "Graph API errors in the last hour.", nil, nil),
	}
}

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.jobs, c.oldest, c.webhookBacklog, c.webhookErrors, c.metaErrors} {
		ch <- d
	}
}

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.snapshot()
	if !s.ok {
		return // the database is unreachable; ecogo_db_pool_* and readiness already show it
	}
	for _, j := range s.jobs {
		ch <- prometheus.MustNewConstMetric(c.jobs, prometheus.GaugeValue, j.count, j.queue, j.state)
		if j.state == "available" || j.state == "retryable" {
			ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, j.oldestSecs, j.queue)
		}
	}
	ch <- prometheus.MustNewConstMetric(c.webhookBacklog, prometheus.GaugeValue, s.webhookBacklog)
	ch <- prometheus.MustNewConstMetric(c.webhookErrors, prometheus.GaugeValue, s.webhookErrors)
	ch <- prometheus.MustNewConstMetric(c.metaErrors, prometheus.GaugeValue, s.metaErrors)
}

func (c *queueCollector) snapshot() snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetched) < cacheFor {
		return c.snap
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var s snapshot
	err := c.db.Global(ctx, func(_ *dbq.Queries, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT queue, state::text, count(*)::float8,
				coalesce(extract(epoch FROM now() - min(scheduled_at)), 0)::float8
			FROM river_job WHERE state IN ('available', 'retryable', 'running', 'scheduled', 'discarded')
			GROUP BY queue, state`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j jobRow
			if err := rows.Scan(&j.queue, &j.state, &j.count, &j.oldestSecs); err != nil {
				return err
			}
			s.jobs = append(s.jobs, j)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM meta_webhook_events WHERE processed_at IS NULL AND received_at < now() - interval '2 minutes')::float8,
				(SELECT count(*) FROM meta_webhook_events WHERE error IS NOT NULL AND received_at > now() - interval '1 hour')::float8,
				(SELECT count(*) FROM meta_api_errors WHERE occurred_at > now() - interval '1 hour')::float8`).
			Scan(&s.webhookBacklog, &s.webhookErrors, &s.metaErrors)
	})
	s.ok = err == nil
	c.snap, c.fetched = s, time.Now()
	return s
}
