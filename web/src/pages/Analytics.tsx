import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useMe, usePhoneNumbers } from "../api/hooks";
import type { UsageCounts, UsageReport } from "../api/types";
import { daysAgo, percent, rupees, scale } from "../lib/analytics";

const RANGES = [7, 30, 90] as const;

export default function Analytics() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canView = role === "owner" || role === "admin";
  const numbers = usePhoneNumbers();
  const [days, setDays] = useState<(typeof RANGES)[number]>(30);
  const [phone, setPhone] = useState("");

  const qs = new URLSearchParams({ from: daysAgo(days - 1) });
  if (phone) qs.set("phone_number_id", phone);
  const report = useQuery({
    queryKey: ["analytics", qs.toString()],
    queryFn: () => api<UsageReport>("GET", `/internal/analytics?${qs}`),
    enabled: canView,
    refetchInterval: 60_000,
  });

  if (role && !canView) {
    return (
      <section>
        <h1>{t("analytics.title")}</h1>
        <div className="card muted">{t("analytics.ownersOnly")}</div>
      </section>
    );
  }

  const r = report.data;
  const numberName = (id: string) => numbers.data?.find((n) => n.id === id)?.display_phone_number ?? id;
  return (
    <section>
      <div className="page-head">
        <h1>{t("analytics.title")}</h1>
      </div>
      <div className="filters">
        <div className="segmented">
          {RANGES.map((d) => (
            <button key={d} className={d === days ? "on" : ""} onClick={() => setDays(d)}>{t("analytics.lastDays", { count: d })}</button>
          ))}
        </div>
        <select value={phone} onChange={(e) => setPhone(e.target.value)}>
          <option value="">{t("analytics.allNumbers")}</option>
          {numbers.data?.map((n) => <option key={n.id} value={n.id}>{n.display_phone_number}</option>)}
        </select>
      </div>

      {report.isLoading && <div className="card muted">{t("common.loading")}</div>}
      {r && (
        <>
          <div className="kpis">
            <Kpi label={t("analytics.sent")} value={r.totals.sent} />
            <Kpi label={t("analytics.delivered")} value={percent(r.totals.delivered, r.totals.sent)} hint={String(r.totals.delivered)} />
            <Kpi label={t("analytics.read")} value={percent(r.totals.read, r.totals.delivered)} hint={String(r.totals.read)} />
            <Kpi label={t("analytics.failed")} value={r.totals.failed} />
            <Kpi label={t("analytics.received")} value={r.totals.received} />
            <Kpi
              label={t("analytics.cost")}
              value={rupees(r.totals.est_cost_minor)}
              hint={t("analytics.billable", { count: r.totals.billable })}
            />
          </div>
          {r.unpriced_billable > 0 && <div className="muted small">{t("analytics.unpriced", { count: r.unpriced_billable })}</div>}

          <div className="card">
            <h2>{t("analytics.perDay")}</h2>
            <DayChart days={r.days} />
            <div className="ticks small muted"><span>{r.from}</span><span>{r.to}</span></div>
            <div className="legend small">
              <span className="sw sw-sent" /> {t("analytics.sent")} <span className="sw sw-received" /> {t("analytics.received")}
            </div>
          </div>

          <div className="grid-2">
            <Breakdown title={t("analytics.byCategory")} rows={r.by_category} label={(k) => t(`analytics.cat_${k}`, { defaultValue: k })} cost />
            <Breakdown title={t("analytics.byOrigin")} rows={r.by_origin} label={(k) => t(`analytics.origin_${k}`, { defaultValue: k })} />
            <Breakdown title={t("analytics.byCountry")} rows={r.by_country} label={(k) => (k === "other" ? t("analytics.otherCountries") : k)} cost />
            <Breakdown title={t("analytics.byNumber")} rows={r.by_number} label={numberName} cost />
          </div>
          <p className="muted small">{t("analytics.note", { tz: r.time_zone })}</p>
        </>
      )}
    </section>
  );
}

function Kpi({ label, value, hint }: { label: string; value: string | number; hint?: string }) {
  return (
    <div className="card kpi">
      <div className="muted small">{label}</div>
      <div className="kpi-value">{value}</div>
      {hint && <div className="muted small">{hint}</div>}
    </div>
  );
}

function DayChart({ days }: { days: (UsageCounts & { day: string })[] }) {
  const h = 140;
  const w = Math.max(days.length * 14, 280);
  const bw = w / days.length;
  const all = scale([...days.map((d) => d.sent), ...days.map((d) => d.received)]);
  const sent = all.slice(0, days.length);
  const recv = all.slice(days.length);
  return (
    <svg className="chart" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" role="img">
      {days.map((d, i) => (
        <g key={d.day}>
          <title>{`${d.day}: ${d.sent} sent, ${d.received} received`}</title>
          <rect className="bar-sent" x={i * bw + bw * 0.1} y={h - sent[i] * h} width={bw * 0.4} height={sent[i] * h} />
          <rect className="bar-received" x={i * bw + bw * 0.5} y={h - recv[i] * h} width={bw * 0.4} height={recv[i] * h} />
        </g>
      ))}
      <line x1="0" x2={w} y1={h} y2={h} className="axis" />
    </svg>
  );
}

function Breakdown({ title, rows, label, cost }: {
  title: string;
  rows: (UsageCounts & { key: string })[];
  label: (k: string) => string;
  cost?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="card table-wrap breakdown">
      <h2>{title}</h2>
      {rows.length === 0 ? (
        <div className="muted">{t("analytics.none")}</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th />
              <th>{t("analytics.sent")}</th>
              <th>{t("analytics.received")}</th>
              {cost && <th>{t("analytics.cost")}</th>}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.key}>
                <td>{label(r.key)}</td>
                <td>{r.sent}</td>
                <td>{r.received}</td>
                {cost && <td>{rupees(r.est_cost_minor)}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
