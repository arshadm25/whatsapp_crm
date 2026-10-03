package admin

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// ListedTenant is a workspace in the console's list, with the columns the list shows.
type ListedTenant struct {
	Tenant
	WabaID             *string `json:"waba_id"`
	WabaCount          int32   `json:"waba_count"`
	PlanCode           *string `json:"plan_code"`
	SubscriptionStatus *string `json:"subscription_status"`
	NumberCount        int32   `json:"number_count"`
	Messages30d        int32   `json:"messages_30d"`
	// WorstQuality is the lowest quality rating among the workspace's numbers; null when none is rated.
	WorstQuality *string `json:"worst_quality"`
}

func nonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// listed adds the list columns to a page of workspaces with one query.
func listed(q *dbq.Queries, r *http.Request, rows []dbq.Tenant) ([]ListedTenant, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, t := range rows {
		ids[i] = t.ID
	}
	extra, err := q.AdminTenantOverview(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	by := map[uuid.UUID]dbq.AdminTenantOverviewRow{}
	for _, e := range extra {
		by[e.TenantID] = e
	}
	out := make([]ListedTenant, len(rows))
	for i, t := range rows {
		e := by[t.ID]
		out[i] = ListedTenant{Tenant: tenantView(t), WabaID: nonEmpty(e.WabaID), WabaCount: e.WabaCount,
			PlanCode: nonEmpty(e.PlanCode), SubscriptionStatus: nonEmpty(e.SubscriptionStatus), NumberCount: e.Numbers,
			Messages30d: e.Messages30d, WorstQuality: nonEmpty(e.WorstQuality)}
	}
	return out, nil
}

// hoursParam reads an optional time window in hours (1 to 168).
func hoursParam(r *http.Request, def int) (int, error) {
	v := r.URL.Query().Get("hours")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 168 {
		return 0, httpx.BadRequest("hours", "hours must be between 1 and 168.")
	}
	return n, nil
}

// Overview is the console's KPI cards.
type Overview struct {
	Tenants struct {
		Active      int32 `json:"active"`
		NewThisWeek int32 `json:"new_this_week"`
		Suspended   int32 `json:"suspended"`
	} `json:"tenants"`
	MetaErrors struct {
		Hours    int   `json:"hours"`
		Current  int32 `json:"current"`
		Previous int32 `json:"previous"` // the same length of time just before
	} `json:"meta_errors"`
}

func (s *Service) overview(w http.ResponseWriter, r *http.Request) error {
	hours, err := hoursParam(r, 24)
	if err != nil {
		return err
	}
	window := time.Duration(hours) * time.Hour
	since := s.now().Add(-window)
	var out Overview
	out.MetaErrors.Hours = hours
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		k, err := q.AdminTenantKPIs(r.Context())
		if err != nil {
			return err
		}
		out.Tenants.Active, out.Tenants.NewThisWeek, out.Tenants.Suspended = k.Active, k.NewThisWeek, k.Suspended
		c, err := q.AdminMetaErrorCounts(r.Context(), dbq.AdminMetaErrorCountsParams{Since: since, PreviousSince: since.Add(-window)})
		out.MetaErrors.Current, out.MetaErrors.Previous = c.Current, c.Previous
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// MetaErrorGroup is one kind of Meta API error in a time window.
type MetaErrorGroup struct {
	Code       *int32    `json:"code"`
	Subcode    *int32    `json:"subcode"`
	HTTPStatus int32     `json:"http_status"`
	Message    string    `json:"message"`
	Count      int32     `json:"count"`
	Tenants    int32     `json:"tenants"`
	LastAt     time.Time `json:"last_at"`
}

// metaErrorSummary groups recent Meta API errors by code, most frequent first.
func (s *Service) metaErrorSummary(w http.ResponseWriter, r *http.Request) error {
	hours, err := hoursParam(r, 24)
	if err != nil {
		return err
	}
	tid, err := optionalUUID(r, "tenant_id")
	if err != nil {
		return err
	}
	out := []MetaErrorGroup{}
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		rows, err := q.AdminMetaErrorGroups(r.Context(), dbq.AdminMetaErrorGroupsParams{
			Since: s.now().Add(-time.Duration(hours) * time.Hour), TenantID: tid})
		for _, g := range rows {
			out = append(out, MetaErrorGroup{Code: g.Code, Subcode: g.Subcode, HTTPStatus: g.HttpStatus, Message: g.Message,
				Count: g.N, Tenants: g.Tenants, LastAt: g.LastAt})
		}
		return err
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"hours": hours, "data": out})
	return nil
}

// exportAuditLog downloads the filtered audit log as CSV, newest first, and records the export.
func (s *Service) exportAuditLog(w http.ResponseWriter, r *http.Request) error {
	arg, err := auditFilter(r)
	if err != nil {
		return err
	}
	arg.Lim = exportLimit
	var rows []dbq.AdminAuditLogRow
	err = s.db.Global(r.Context(), func(q *dbq.Queries, _ pgx.Tx) error {
		var err error
		if rows, err = q.AdminAuditLog(r.Context(), arg); err != nil {
			return err
		}
		return s.record(r, q, arg.TenantID, "audit_log.export", "audit_log", fmt.Sprintf("%d rows", len(rows)), nil)
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ecogo-audit-log.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Time (UTC)", "Workspace ID", "Actor type", "Actor", "Action", "Target type", "Target", "Reason", "IP"})
	str := func(v *string) string {
		if v == nil {
			return ""
		}
		return safeCell(*v)
	}
	for _, a := range rows {
		var tenant, ip string
		if a.TenantID != nil {
			tenant = a.TenantID.String()
		}
		if a.Ip != nil {
			ip = a.Ip.String()
		}
		actor := str(a.ActorEmail)
		if actor == "" && a.ActorID != nil {
			actor = a.ActorID.String()
		}
		_ = cw.Write([]string{a.OccurredAt.UTC().Format(time.RFC3339), tenant, string(a.ActorType), actor, safeCell(a.Action),
			str(a.TargetType), str(a.TargetID), str(a.Reason), ip})
	}
	cw.Flush()
	return nil
}
