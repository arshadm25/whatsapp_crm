import { Fragment, useState } from "react";
import { useTranslation } from "react-i18next";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { useMe } from "../../api/hooks";
import type { APIKey, APILogEntry, APILogSummary } from "../../api/types";
import { Pager, usePager, type PageSize } from "../../components/Pager";
import { canUseDevelopers, DevTabs, RangePicker, ShareRows, StackedBars, Stat, type DevRange } from "../../components/DevCharts";
import { buckets, fill, ms, pct, statusTone } from "../../lib/devAnalytics";
import { shortTime } from "../../lib/time";

const STATUSES = ["", "2xx", "4xx", "5xx", "errors"] as const;
const METHODS = ["", "GET", "POST", "PATCH", "PUT", "DELETE"] as const;

// Developers > API logs: request volume, errors, status codes and latency, then every request
// made with an API key (kept for 30 days) with filters.
export default function ApiLogs() {
  const { t } = useTranslation();
  const allowed = canUseDevelopers(useMe().data?.tenant?.role);
  const [days, setDays] = useState<DevRange>(7);
  const summary = useQuery({
    queryKey: ["api-log-summary", days],
    queryFn: () => api<APILogSummary>("GET", `/internal/developers/logs/summary?days=${days}`),
    enabled: allowed,
    refetchInterval: 60_000,
  });
  const keys = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => (await api<{ data: APIKey[] }>("GET", "/internal/developers/api-keys")).data,
    enabled: allowed,
  });

  if (!allowed) return <section><h1>{t("nav.apiLogs")}</h1><div className="card muted">{t("developers.noAccess")}</div></section>;

  const s = summary.data;
  const list = s ? buckets(s) : [];
  const rangeLabel = days === 1 ? t("devpages.last24h") : t("analytics.days", { count: days });
  const statusRows = (s?.by_status ?? []).map((x) => ({ key: String(x.status), label: `${x.status} ${statusText(x.status)}`, n: x.n }));
  const toneOf = (k: string) => ({ ok: "var(--accent)", wa: "#fe9a00", er: "var(--danger)" })[statusTone(Number(k))];

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("nav.apiLogs")}</h1>
          <p className="sub">{t("devpages.logsIntro", { count: s?.retention_days ?? 30 })}</p>
        </div>
        <div className="actions">
          <RangePicker value={days} onChange={setDays} />
        </div>
      </div>
      <DevTabs />
      <div className="stack">
        <div className="grid g4">
          <Stat label={t("devpages.requests")} value={s ? s.calls.toLocaleString() : "—"} icon="activity" foot={rangeLabel} />
          <Stat label={t("devpages.errorRate")} value={s ? pct(s.errors, s.calls) : "—"} icon="alert" tone={s && s.errors > 0 ? "rd" : "gy"}
            foot={t("devpages.errorsCount", { count: s?.errors ?? 0 })} />
          <Stat label={t("devpages.p50")} value={s ? ms(s.p50_ms) : "—"} icon="clock" tone="bl" foot={t("devpages.p50Foot")} />
          <Stat label={t("devpages.p95")} value={s ? ms(s.p95_ms) : "—"} icon="clock" tone="am" foot={t("devpages.maxLatency", { time: s ? ms(s.max_ms) : "—" })} />
        </div>

        <div className="split">
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.requestsOverTime")}</h2><p>{s?.hourly ? t("devpages.byHour") : t("devpages.byDay")}</p></div></div>
            <div className="cb">
              {s ? (
                <StackedBars buckets={list} label={t("devpages.requestsOverTime")} series={[
                  { key: "ok", label: t("devpages.successful"), color: "var(--accent)", values: fill(s, list, s.points, (p) => p.calls - p.errors) },
                  { key: "err", label: t("devpages.errors"), color: "var(--danger)", values: fill(s, list, s.points, (p) => p.errors) },
                ]} />
              ) : <div className="muted">{t("common.loading")}</div>}
            </div>
          </div>
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.statusCodes")}</h2><p>{rangeLabel}</p></div></div>
            <div className="cb cats-list">
              <ShareRows rows={statusRows} total={s?.calls ?? 0} tone={toneOf} />
            </div>
          </div>
        </div>

        {!!s?.by_route.length && (
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.topEndpoints")}</h2><p>{t("devpages.topEndpointsSub")}</p></div></div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{t("devpages.endpoint")}</th>
                    <th className="r">{t("devpages.requests")}</th>
                    <th className="r">{t("devpages.errors")}</th>
                    <th className="r">{t("devpages.errorRate")}</th>
                    <th className="r">{t("devpages.p95")}</th>
                  </tr>
                </thead>
                <tbody>
                  {s.by_route.map((r) => (
                    <tr key={`${r.method} ${r.route}`}>
                      <td className="k"><span className="method">{r.method}</span>{r.route}</td>
                      <td className="r num-t">{r.calls.toLocaleString()}</td>
                      <td className="r num-t">{r.errors ? <span style={{ color: "var(--danger)" }}>{r.errors.toLocaleString()}</span> : 0}</td>
                      <td className="r num-t">{pct(r.errors, r.calls)}</td>
                      <td className="r num-t">{ms(r.p95_ms)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        <RequestLog keys={keys.data ?? []} />
      </div>
    </section>
  );
}

function RequestLog({ keys }: { keys: APIKey[] }) {
  const { t } = useTranslation();
  const [key, setKey] = useState("");
  const [status, setStatus] = useState<(typeof STATUSES)[number]>("");
  const [method, setMethod] = useState<(typeof METHODS)[number]>("");
  const [path, setPath] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const [size, setSize] = useState<PageSize>(50);
  const params = new URLSearchParams({ limit: String(size) });
  if (key) params.set("api_key_id", key);
  if (status) params.set("status", status);
  if (method) params.set("method", method);
  if (path.trim()) params.set("path", path.trim());
  const pager = usePager<number>(params.toString());
  const q = useQuery({
    queryKey: ["api-logs", params.toString(), pager.cursor],
    queryFn: () =>
      api<{ data: APILogEntry[]; next_before: number | null; total: number }>(
        "GET", `/internal/developers/logs?${params}${pager.cursor ? `&before=${pager.cursor}` : ""}`),
    placeholderData: keepPreviousData,
    // Only the first page picks up new requests; later pages stay put while you read them.
    refetchInterval: pager.page === 1 ? 15_000 : false,
  });
  const rows = q.data?.data ?? [];
  const dayAgo = Date.now() - 24 * 3600_000;

  return (
    <div className="card flush">
      <div className="chd"><div><h2>{t("devpages.requestLog")}</h2><p>{t("devpages.requestLogSub")}</p></div></div>
      <div className="cb" style={{ paddingBottom: 0 }}>
        <div className="filters">
          <input type="search" value={path} onChange={(e) => setPath(e.target.value)} placeholder={t("devpages.searchPath")} aria-label={t("devpages.searchPath")} />
          <select value={key} onChange={(e) => setKey(e.target.value)} aria-label={t("developers.key")}>
            <option value="">{t("devpages.allKeys")}</option>
            {keys.map((k) => <option key={k.id} value={k.id}>{k.name} ({k.prefix}••••)</option>)}
          </select>
          <select value={status} onChange={(e) => setStatus(e.target.value as (typeof STATUSES)[number])} aria-label={t("developers.status")}>
            {STATUSES.map((v) => <option key={v} value={v}>{t(`devpages.status_${v || "all"}`)}</option>)}
          </select>
          <select value={method} onChange={(e) => setMethod(e.target.value as (typeof METHODS)[number])} aria-label={t("devpages.method")}>
            {METHODS.map((v) => <option key={v} value={v}>{v || t("devpages.allMethods")}</option>)}
          </select>
        </div>
      </div>
      {q.isLoading && <div className="cb muted">{t("common.loading")}</div>}
      {q.data && !rows.length && <div className="cb muted">{t("devpages.noRequests")}</div>}
      {!!rows.length && (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("developers.time")}</th>
                <th>{t("devpages.request")}</th>
                <th>{t("developers.status")}</th>
                <th className="r">{t("devpages.latency")}</th>
                <th>{t("developers.key")}</th>
                <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((l) => (
                <Fragment key={l.id}>
                  <tr className="log-row">
                    <td title={new Date(l.created_at).toLocaleString()}>
                      {new Date(l.created_at).getTime() >= dayAgo ? shortTime(l.created_at) : new Date(l.created_at).toLocaleString()}
                    </td>
                    <td className="k"><span className="method">{l.method}</span>{l.path}</td>
                    <td><span className={`pill ${statusTone(l.status)}`}>{l.status}</span></td>
                    <td className="r num-t">{l.duration_ms < 1 ? "<1 ms" : ms(l.duration_ms)}</td>
                    <td>{l.key_name}</td>
                    <td className="r">
                      <button className="lnk" onClick={() => setOpen(open === l.id ? null : l.id)} aria-expanded={open === l.id}>
                        {open === l.id ? t("devpages.hideDetails") : t("devpages.details")}
                      </button>
                    </td>
                  </tr>
                  {open === l.id && (
                    <tr>
                      <td colSpan={6} className="log-detail">
                        <dl>
                          <dt>{t("devpages.route")}</dt><dd className="k">{l.route || "—"}</dd>
                          <dt>{t("devpages.requestId")}</dt><dd className="k">{l.request_id || "—"}</dd>
                          <dt>{t("developers.key")}</dt><dd className="k">{l.key_name} · {l.key_prefix}••••</dd>
                          <dt>{t("developers.time")}</dt><dd>{new Date(l.created_at).toLocaleString()}</dd>
                        </dl>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {q.data && (rows.length > 0 || pager.page > 1) && (
        <Pager page={pager.page} size={size} shown={rows.length} total={q.data.total} hasNext={!!q.data.next_before}
          busy={q.isFetching && q.isPlaceholderData}
          onPrev={pager.prev} onFirst={pager.first} onSize={setSize}
          onNext={() => { if (q.data?.next_before) pager.next(q.data.next_before); }} />
      )}
    </div>
  );
}

// Short reason phrases for the status codes the API returns.
function statusText(code: number) {
  const m: Record<number, string> = {
    200: "OK", 201: "Created", 202: "Accepted", 204: "No Content", 400: "Bad Request", 401: "Unauthorized", 402: "Payment Required",
    403: "Forbidden", 404: "Not Found", 409: "Conflict", 413: "Too Large", 422: "Unprocessable", 429: "Too Many Requests",
    500: "Server Error", 502: "Bad Gateway", 503: "Unavailable", 504: "Timeout",
  };
  return m[code] ?? "";
}
