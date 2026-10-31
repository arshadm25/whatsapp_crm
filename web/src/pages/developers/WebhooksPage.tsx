import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { useMe } from "../../api/hooks";
import type { WebhookEndpoint, WebhookReport } from "../../api/types";
import Webhooks from "../../components/Webhooks";
import { canUseDevelopers, DevTabs, RangePicker, StackedBars, Stat, type DevRange } from "../../components/DevCharts";
import { buckets, fill, pct } from "../../lib/devAnalytics";

// Developers > Webhooks: delivery analytics, then the endpoints and their delivery log.
export default function WebhooksPage() {
  const { t } = useTranslation();
  const allowed = canUseDevelopers(useMe().data?.tenant?.role);
  const [days, setDays] = useState<DevRange>(7);
  const report = useQuery({
    queryKey: ["webhook-usage", days],
    queryFn: () => api<WebhookReport>("GET", `/internal/developers/webhook-usage?days=${days}`),
    enabled: allowed,
    refetchInterval: 60_000,
  });
  const endpoints = useQuery({
    queryKey: ["webhook-endpoints"],
    queryFn: async () => (await api<{ data: WebhookEndpoint[] }>("GET", "/v1/webhook-endpoints")).data,
    enabled: allowed,
  });

  if (!allowed) return <section><h1>{t("nav.webhooks")}</h1><div className="card muted">{t("developers.noAccess")}</div></section>;

  const r = report.data;
  const tot = r?.totals;
  const tried = tot ? tot.deliveries - tot.pending : 0;
  const list = r ? buckets(r) : [];
  const rangeLabel = days === 1 ? t("devpages.last24h") : t("analytics.days", { count: days });
  const urlOf = (id: string) => endpoints.data?.find((e) => e.id === id)?.url ?? t("devpages.deletedEndpoint");

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("nav.webhooks")}</h1>
          <p className="sub">{t("devpages.webhooksIntro")}</p>
        </div>
        <div className="actions">
          <RangePicker value={days} onChange={setDays} />
        </div>
      </div>
      <DevTabs />
      <div className="stack">
        <div className="grid g4">
          <Stat label={t("developers.deliveries")} value={tot ? tot.deliveries.toLocaleString() : "—"} icon="zap" foot={rangeLabel} />
          <Stat label={t("devpages.successRate")} value={tot ? pct(tot.succeeded, tried) : "—"} icon="check" tone="bl"
            foot={t("devpages.deliveredCount", { count: tot?.succeeded ?? 0 })} />
          <Stat label={t("devpages.failed")} value={tot ? tot.failed.toLocaleString() : "—"} icon="alert" tone={tot && tot.failed > 0 ? "rd" : "gy"}
            foot={t("devpages.failedFoot", { count: tot?.retrying ?? 0 })} />
          <Stat label={t("devpages.retried")} value={tot ? tot.retried.toLocaleString() : "—"} icon="refresh" tone="am"
            foot={t("devpages.avgAttempts", { n: tot && tried ? (tot.attempts / Math.max(1, tried)).toFixed(2) : "—" })} />
        </div>

        <div className="split">
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.deliveriesOverTime")}</h2><p>{r?.hourly ? t("devpages.byHour") : t("devpages.byDay")}</p></div></div>
            <div className="cb">
              {r ? (
                <StackedBars buckets={list} label={t("devpages.deliveriesOverTime")} series={[
                  { key: "ok", label: t("developers.delivery_succeeded"), color: "var(--accent)", values: fill(r, list, r.points, (p) => p.succeeded) },
                  { key: "wait", label: t("devpages.inProgress"), color: "#fe9a00", values: fill(r, list, r.points, (p) => p.retrying + p.pending) },
                  { key: "dead", label: t("developers.delivery_dead"), color: "var(--danger)", values: fill(r, list, r.points, (p) => p.failed) },
                ]} />
              ) : <div className="muted">{t("common.loading")}</div>}
            </div>
          </div>
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.successByEndpoint")}</h2><p>{t("devpages.successByEndpointSub")}</p></div></div>
            <div className="cb cats-list">
              {!r || !r.by_endpoint.length ? <div className="muted small">{t("devpages.noData")}</div> : r.by_endpoint.map((e) => {
                const done = e.deliveries - e.pending;
                const rate = done ? Math.round((e.succeeded * 100) / done) : 0;
                return (
                  <div key={e.endpoint_id} className="cat" title={urlOf(e.endpoint_id)}>
                    <span className="k" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{hostOf(urlOf(e.endpoint_id))}</span>
                    <div className="bar" style={{ height: 8 }}><span className={rate < 90 ? (rate < 50 ? "rd" : "am") : ""} style={{ width: `${rate}%` }} /></div>
                    <b>{pct(e.succeeded, done)}</b>
                  </div>
                );
              })}
            </div>
          </div>
        </div>

        {!!r?.by_endpoint.length && (
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.perEndpoint")}</h2><p>{rangeLabel}</p></div></div>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{t("developers.url")}</th>
                    <th className="r">{t("developers.deliveries")}</th>
                    <th className="r">{t("devpages.successRate")}</th>
                    <th className="r">{t("devpages.failed")}</th>
                    <th className="r">{t("developers.delivery_retrying")}</th>
                    <th className="r">{t("devpages.retried")}</th>
                  </tr>
                </thead>
                <tbody>
                  {r.by_endpoint.map((e) => (
                    <tr key={e.endpoint_id}>
                      <td className="k" style={{ overflowWrap: "anywhere" }}>{urlOf(e.endpoint_id)}</td>
                      <td className="r num-t">{e.deliveries.toLocaleString()}</td>
                      <td className="r num-t">{pct(e.succeeded, e.deliveries - e.pending)}</td>
                      <td className="r num-t">{e.failed ? <span style={{ color: "var(--danger)" }}>{e.failed.toLocaleString()}</span> : 0}</td>
                      <td className="r num-t">{e.retrying.toLocaleString()}</td>
                      <td className="r num-t">{e.retried.toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        <Webhooks />
      </div>
    </section>
  );
}

function hostOf(url: string) {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}
