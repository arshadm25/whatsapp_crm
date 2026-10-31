package devportal

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/auth"
	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// The API keys, Webhooks and API logs pages each show charts over a range of days. A range of
// one day is shown by hour; longer ranges by day in the workspace's time zone.

// logRetention is how long api_request_log rows are kept (see the retention job).
const logRetention = 30 * 24 * time.Hour

// rangeArgs reads ?days= (1 to max, default def) and returns the start of the range, whether
// buckets are hours, and the workspace's time zone name.
func rangeArgs(r *http.Request, q *dbq.Queries, tenantID uuid.UUID, def, max int, now time.Time) (since time.Time, hourly bool, tz string, days int, err error) {
	days = def
	if v := r.URL.Query().Get("days"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > max {
			return since, false, "", 0, httpx.BadRequest("days", "days must be a whole number from 1 to "+strconv.Itoa(max)+".")
		}
		days = n
	}
	t, err := q.GetTenant(r.Context(), tenantID)
	if err != nil {
		return since, false, "", 0, err
	}
	loc, lerr := time.LoadLocation(t.Timezone)
	if lerr != nil || t.Timezone == "" {
		loc, _ = time.LoadLocation("Asia/Kolkata")
	}
	if loc == nil {
		loc = time.UTC
	}
	if days == 1 {
		return now.Add(-24 * time.Hour).Truncate(time.Hour), true, loc.String(), days, nil
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	return start, false, loc.String(), days, nil
}

// SeriesMeta tells the dashboard how the buckets are laid out.
type SeriesMeta struct {
	From     time.Time `json:"from"`
	Hourly   bool      `json:"hourly"`
	TimeZone string    `json:"time_zone"`
	Days     int       `json:"days"`
}

// ---- API keys ----

// KeyPoint is one key's calls in one bucket.
type KeyPoint struct {
	Bucket   time.Time `json:"bucket"`
	APIKeyID uuid.UUID `json:"api_key_id"`
	Calls    int32     `json:"calls"`
	Errors   int32     `json:"errors"`
}

// KeyUsageReport is the API keys page's chart data and per-key totals for the range.
type KeyUsageReport struct {
	SeriesMeta
	Calls  int32      `json:"calls"`
	Errors int32      `json:"errors"`
	ByKey  []KeyUsage `json:"by_key"`
	Points []KeyPoint `json:"points"`
}

func (s *Service) keyUsage(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := KeyUsageReport{ByKey: []KeyUsage{}, Points: []KeyPoint{}}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		since, hourly, tz, days, err := rangeArgs(r, q, p.TenantID, 7, 90, time.Now())
		if err != nil {
			return err
		}
		out.SeriesMeta = SeriesMeta{From: since, Hourly: hourly, TimeZone: tz, Days: days}
		rows, err := q.APIUsageSeries(r.Context(), dbq.APIUsageSeriesParams{Hourly: hourly, Tz: tz, Since: since})
		if err != nil {
			return err
		}
		byKey := map[uuid.UUID]*KeyUsage{}
		for _, row := range rows {
			out.Points = append(out.Points, KeyPoint{Bucket: row.Bucket, APIKeyID: row.ApiKeyID, Calls: row.Calls, Errors: row.Errors})
			out.Calls += row.Calls
			out.Errors += row.Errors
			k := byKey[row.ApiKeyID]
			if k == nil {
				k = &KeyUsage{APIKeyID: row.ApiKeyID}
				byKey[row.ApiKeyID] = k
			}
			k.Calls += row.Calls
			k.Errors += row.Errors
		}
		for _, k := range byKey {
			out.ByKey = append(out.ByKey, *k)
		}
		sort.Slice(out.ByKey, func(i, j int) bool { return out.ByKey[i].Calls > out.ByKey[j].Calls })
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// ---- Webhooks ----

// DeliveryCounts are webhook deliveries by outcome. Failed deliveries gave up after every
// retry; retried ones needed more than one attempt (whatever their outcome).
type DeliveryCounts struct {
	Deliveries int32 `json:"deliveries"`
	Succeeded  int32 `json:"succeeded"`
	Failed     int32 `json:"failed"`
	Retrying   int32 `json:"retrying"`
	Pending    int32 `json:"pending"`
	Retried    int32 `json:"retried"`
	Attempts   int32 `json:"attempts"`
}

func (c *DeliveryCounts) add(o DeliveryCounts) {
	c.Deliveries += o.Deliveries
	c.Succeeded += o.Succeeded
	c.Failed += o.Failed
	c.Retrying += o.Retrying
	c.Pending += o.Pending
	c.Retried += o.Retried
	c.Attempts += o.Attempts
}

type DeliveryPoint struct {
	Bucket time.Time `json:"bucket"`
	DeliveryCounts
}

type EndpointDeliveries struct {
	EndpointID uuid.UUID `json:"endpoint_id"`
	DeliveryCounts
}

// WebhookReport is the Webhooks page's chart data and per-endpoint totals for the range.
type WebhookReport struct {
	SeriesMeta
	Totals     DeliveryCounts       `json:"totals"`
	Points     []DeliveryPoint      `json:"points"`
	ByEndpoint []EndpointDeliveries `json:"by_endpoint"`
}

func (s *Service) webhookUsage(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := WebhookReport{Points: []DeliveryPoint{}, ByEndpoint: []EndpointDeliveries{}}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		since, hourly, tz, days, err := rangeArgs(r, q, p.TenantID, 7, 90, time.Now())
		if err != nil {
			return err
		}
		out.SeriesMeta = SeriesMeta{From: since, Hourly: hourly, TimeZone: tz, Days: days}
		rows, err := q.DeliverySeries(r.Context(), dbq.DeliverySeriesParams{Hourly: hourly, Tz: tz, Since: since})
		if err != nil {
			return err
		}
		byBucket := map[time.Time]*DeliveryPoint{}
		byEndpoint := map[uuid.UUID]*EndpointDeliveries{}
		for _, row := range rows {
			c := DeliveryCounts{Deliveries: row.Deliveries, Succeeded: row.Succeeded, Failed: row.Failed,
				Retrying: row.Retrying, Pending: row.Pending, Retried: row.Retried, Attempts: row.Attempts}
			out.Totals.add(c)
			b := byBucket[row.Bucket]
			if b == nil {
				b = &DeliveryPoint{Bucket: row.Bucket}
				byBucket[row.Bucket] = b
			}
			b.add(c)
			e := byEndpoint[row.EndpointID]
			if e == nil {
				e = &EndpointDeliveries{EndpointID: row.EndpointID}
				byEndpoint[row.EndpointID] = e
			}
			e.add(c)
		}
		for _, b := range byBucket {
			out.Points = append(out.Points, *b)
		}
		sort.Slice(out.Points, func(i, j int) bool { return out.Points[i].Bucket.Before(out.Points[j].Bucket) })
		for _, e := range byEndpoint {
			out.ByEndpoint = append(out.ByEndpoint, *e)
		}
		sort.Slice(out.ByEndpoint, func(i, j int) bool { return out.ByEndpoint[i].Deliveries > out.ByEndpoint[j].Deliveries })
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// ---- API logs ----

// LogEntry is one public API request.
type LogEntry struct {
	ID         int64     `json:"id"`
	APIKeyID   uuid.UUID `json:"api_key_id"`
	KeyName    string    `json:"key_name"`
	KeyPrefix  string    `json:"key_prefix"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Route      string    `json:"route"`
	Status     int16     `json:"status"`
	DurationMs int32     `json:"duration_ms"`
	RequestID  string    `json:"request_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// listLogs serves the request log, newest first. Filters: api_key_id, status (2xx, 3xx, 4xx,
// 5xx or errors), method, path (contains), before (an id, for the next page), limit. The
// response carries next_before (null on the last page) and total, the matching count.
func (s *Service) listLogs(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	qs := r.URL.Query()
	arg := dbq.ListAPIRequestLogParams{Lim: 50}
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return httpx.BadRequest("limit", "limit must be from 1 to 200.")
		}
		arg.Lim = int32(n)
	}
	if v := qs.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return httpx.BadRequest("before", "before is not valid.")
		}
		arg.Before = &n
	}
	if v := qs.Get("api_key_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return httpx.BadRequest("api_key_id", "api_key_id is not valid.")
		}
		arg.ApiKeyID = &id
	}
	switch v := qs.Get("status"); v {
	case "":
	case "2xx", "3xx", "4xx", "5xx":
		arg.StatusClass = int32(v[0] - '0')
	case "errors":
		arg.ErrorsOnly = true
	default:
		return httpx.BadRequest("status", "status must be 2xx, 3xx, 4xx, 5xx or errors.")
	}
	arg.Method = strings.ToUpper(strings.TrimSpace(qs.Get("method")))
	arg.Path = strings.TrimSpace(qs.Get("path"))
	if len(arg.Path) > 200 {
		arg.Path = arg.Path[:200]
	}
	// ILIKE wildcards in the search text are matched literally.
	arg.Path = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(arg.Path)
	out := []LogEntry{}
	var total int32
	var next *int64
	lim := arg.Lim
	arg.Lim = lim + 1 // one extra row tells whether there is a next page
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.ListAPIRequestLog(r.Context(), arg)
		if err != nil {
			return err
		}
		if len(rows) > int(lim) {
			rows = rows[:lim]
			id := rows[len(rows)-1].ID
			next = &id
		}
		for _, l := range rows {
			out = append(out, LogEntry{ID: l.ID, APIKeyID: l.ApiKeyID, KeyName: l.KeyName, KeyPrefix: l.KeyPrefix,
				Method: l.Method, Path: l.Path, Route: l.Route, Status: l.Status, DurationMs: l.DurationMs,
				RequestID: l.RequestID, CreatedAt: l.CreatedAt})
		}
		total, err = q.CountAPIRequestLog(r.Context(), dbq.CountAPIRequestLogParams{ApiKeyID: arg.ApiKeyID,
			StatusClass: arg.StatusClass, ErrorsOnly: arg.ErrorsOnly, Method: arg.Method, Path: arg.Path})
		return err
	})
	if err != nil {
		return err
	}
	// total counts every matching request kept, for "1–50 of 2,556".
	resp := map[string]any{"data": out, "next_before": next, "total": total}
	httpx.JSON(w, http.StatusOK, resp)
	return nil
}

type StatusCount struct {
	Status int16 `json:"status"`
	N      int32 `json:"n"`
}

type LogPoint struct {
	Bucket time.Time `json:"bucket"`
	Calls  int32     `json:"calls"`
	Errors int32     `json:"errors"`
	P95Ms  float64   `json:"p95_ms"`
}

type RouteStats struct {
	Method string  `json:"method"`
	Route  string  `json:"route"`
	Calls  int32   `json:"calls"`
	Errors int32   `json:"errors"`
	P95Ms  float64 `json:"p95_ms"`
}

// LogSummary is the API logs page's analytics for the range (at most the 30 days kept).
type LogSummary struct {
	SeriesMeta
	Calls    int32         `json:"calls"`
	Errors   int32         `json:"errors"`
	P50Ms    float64       `json:"p50_ms"`
	P95Ms    float64       `json:"p95_ms"`
	MaxMs    int32         `json:"max_ms"`
	ByStatus []StatusCount `json:"by_status"`
	Points   []LogPoint    `json:"points"`
	ByRoute  []RouteStats  `json:"by_route"`
	// RetentionDays is how long single requests are kept.
	RetentionDays int `json:"retention_days"`
}

func (s *Service) logSummary(w http.ResponseWriter, r *http.Request) error {
	p, _ := auth.PrincipalFrom(r.Context())
	out := LogSummary{ByStatus: []StatusCount{}, Points: []LogPoint{}, ByRoute: []RouteStats{}, RetentionDays: int(logRetention / (24 * time.Hour))}
	err := s.db.InTenant(r.Context(), p.TenantID, func(q *dbq.Queries, _ pgx.Tx) error {
		ctx := r.Context()
		since, hourly, tz, days, err := rangeArgs(r, q, p.TenantID, 7, out.RetentionDays, time.Now())
		if err != nil {
			return err
		}
		out.SeriesMeta = SeriesMeta{From: since, Hourly: hourly, TimeZone: tz, Days: days}
		tot, err := q.APIRequestLogTotals(ctx, since)
		if err != nil {
			return err
		}
		out.Calls, out.Errors, out.P50Ms, out.P95Ms, out.MaxMs = tot.Calls, tot.Errors, tot.P50Ms, tot.P95Ms, tot.MaxMs
		statuses, err := q.APIRequestLogByStatus(ctx, since)
		if err != nil {
			return err
		}
		for _, st := range statuses {
			out.ByStatus = append(out.ByStatus, StatusCount{Status: st.Status, N: st.N})
		}
		points, err := q.APIRequestLogSeries(ctx, dbq.APIRequestLogSeriesParams{Hourly: hourly, Tz: tz, Since: since})
		if err != nil {
			return err
		}
		for _, pt := range points {
			out.Points = append(out.Points, LogPoint{Bucket: pt.Bucket, Calls: pt.Calls, Errors: pt.Errors, P95Ms: pt.P95Ms})
		}
		routes, err := q.APIRequestLogByRoute(ctx, since)
		if err != nil {
			return err
		}
		for _, rt := range routes {
			out.ByRoute = append(out.ByRoute, RouteStats{Method: rt.Method, Route: rt.Route, Calls: rt.Calls, Errors: rt.Errors, P95Ms: rt.P95Ms})
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
