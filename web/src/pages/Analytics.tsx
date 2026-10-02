import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useMe, usePhoneNumbers } from "../api/hooks";
import type { UsageCounts, UsageReport } from "../api/types";
import { daysAgo, percent, rupees } from "../lib/analytics";
import Icon, { type IconName } from "../components/Icon";
import { shortDate } from "../lib/time";

const RANGES = [7, 30, 90] as const;

export default function Analytics() {
  const { t } = useTranslation();
  const me = useMe().data;
  const role = me?.tenant?.role;
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

  // Export: one CSV row per day, built in the browser from the loaded report.
  const exportCsv = () => {
    if (!r) return;
    const head = ["day", "sent", "delivered", "read", "failed", "received", "billable", "est_cost_inr"];
    const rows = r.days.map((d) => [d.day, d.sent, d.delivered, d.read, d.failed, d.received, d.billable, (d.est_cost_minor / 100).toFixed(2)]);
    const csv = [head, ...rows].map((row) => row.join(",")).join("\n");
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    a.download = `analytics-${r.from}-${r.to}.csv`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("analytics.title")}</h1>
          <p className="sub">{t("analytics.intro", { name: me?.tenant?.name ?? "" })}</p>
        </div>
        <div className="actions">
          <div className="segmented" role="radiogroup" aria-label={t("analytics.range")}>
            {RANGES.map((d) => (
              <button key={d} className={d === days ? "on" : ""} onClick={() => setDays(d)}>{t("analytics.days", { count: d })}</button>
            ))}
          </div>
          {(numbers.data?.length ?? 0) > 1 && (
            <select className="role" value={phone} onChange={(e) => setPhone(e.target.value)} aria-label={t("analytics.allNumbers")}>
              <option value="">{t("analytics.allNumbers")}</option>
              {numbers.data?.map((n) => <option key={n.id} value={n.id}>{n.display_phone_number}</option>)}
            </select>
          )}
          <button onClick={exportCsv} disabled={!r}><Icon name="download" size="s" />{t("analytics.export")}</button>
        </div>
      </div>

      {report.isLoading && <div className="card muted">{t("common.loading")}</div>}
      {r && (
        <div className="stack">
          <div className="grid g4">
            <Stat label={t("analytics.kpiSent")} value={r.totals.sent.toLocaleString()} icon="send"
              foot={t("analytics.kpiSentFoot", { delivered: percent(r.totals.delivered, r.totals.sent), read: percent(r.totals.read, r.totals.delivered) })} />
            <Stat label={t("analytics.kpiReceived")} value={r.totals.received.toLocaleString()} icon="inbox" tone="bl" foot={t("analytics.kpiReceivedFoot")} />
            <Stat label={t("analytics.kpiCost")} value={rupees(r.totals.est_cost_minor)} icon="activity" tone="am"
              foot={r.unpriced_billable > 0 ? t("analytics.unpriced", { count: r.unpriced_billable }) : t("analytics.kpiCostFoot", { count: r.totals.billable })} />
            <Stat label={t("analytics.kpiFailed")} value={r.totals.failed.toLocaleString()} icon="alert" tone={r.totals.failed > 0 ? "rd" : "gy"}
              foot={t("analytics.kpiFailedFoot", { pct: percent(r.totals.failed, r.totals.sent) })} />
          </div>

          <div className="split">
            <div className="card flush">
              <div className="chd">
                <div><h2>{t("analytics.overTime")}</h2><p>{t("analytics.overTimeSub", { count: days })}</p></div>
                <div className="lg-row">
                  <span className="lg"><i style={{ background: "var(--accent)" }} />{t("analytics.sent")}</span>
                  <span className="lg"><i style={{ background: "#99a1af" }} />{t("analytics.received")}</span>
                </div>
              </div>
              <div className="cb">
                {r.days.length > 1 ? <LineChart days={r.days} /> : <div className="muted">{t("analytics.none")}</div>}
              </div>
            </div>
            <div className="card flush">
              <div className="chd"><div><h2>{t("analytics.byCategory")}</h2><p>{t("analytics.shareOfSent")}</p></div></div>
              <div className="cb cats">
                <Shares rows={r.by_category} total={r.totals.sent} label={(k) => t(`analytics.cat_${k}`, { defaultValue: k })} />
                <hr className="dv" />
                <span className="sl">{t("analytics.byOrigin")}</span>
                <Shares rows={r.by_origin} total={r.totals.sent + r.totals.received} label={(k) => t(`analytics.origin_${k}`, { defaultValue: k })} all />
              </div>
            </div>
          </div>

          <div className="grid g2">
            <Breakdown
              title={t("analytics.costByCountry")} sub={t("analytics.costByCountrySub")}
              first={t("analytics.country")} rows={r.by_country} total={r.totals.sent}
              label={(k) => (k === "other" ? t("analytics.otherCountries") : k)}
            />
            <Breakdown
              title={t("analytics.byNumber")} sub={t("analytics.byNumberSub")}
              first={t("numbers.number")} rows={r.by_number} total={r.totals.sent} label={numberName}
            />
          </div>
          <p className="muted small">{t("analytics.note", { tz: r.time_zone })}</p>
        </div>
      )}
    </section>
  );
}

function Stat({ label, value, foot, icon, tone = "" }: { label: string; value: string; foot: string; icon: IconName; tone?: string }) {
  return (
    <div className="card stat">
      <div className="sh"><span className="sl">{label}</span><span className={`ic ${tone}`}><Icon name={icon} size="s" /></span></div>
      <span className="sv">{value}</span>
      <span className="sf">{foot}</span>
    </div>
  );
}

// Share bars: each key's part of the total, largest first.
function Shares({ rows, total, label, all }: { rows: (UsageCounts & { key: string })[]; total: number; label: (k: string) => string; all?: boolean }) {
  const { t } = useTranslation();
  const sorted = [...rows].map((r) => ({ key: r.key, n: all ? r.sent + r.received : r.sent })).filter((r) => r.n > 0).sort((a, b) => b.n - a.n);
  if (!sorted.length) return <div className="muted small">{t("analytics.none")}</div>;
  return (
    <>
      {sorted.map((r) => {
        const pct = total > 0 ? Math.round((r.n * 100) / total) : 0;
        return (
          <div key={r.key} className="cat">
            {label(r.key)}
            <div className="bar" style={{ height: 8 }}><span style={{ width: `${pct}%` }} /></div>
            <b>{pct}%</b>
          </div>
        );
      })}
    </>
  );
}

// Daily sent (area + line) and received (dashed) over the range, with the latest value called out.
function LineChart({ days }: { days: (UsageCounts & { day: string })[] }) {
  const { t } = useTranslation();
  const W = 720, H = 240, L = 50, R = 700, TOP = 20, BOT = 200;
  const max = Math.max(1, ...days.map((d) => Math.max(d.sent, d.received)));
  const x = (i: number) => L + (i * (R - L)) / (days.length - 1);
  const y = (v: number) => BOT - (v / max) * (BOT - TOP);
  const pts = (f: (d: UsageCounts) => number) => days.map((d, i) => `${x(i).toFixed(1)},${y(f(d)).toFixed(1)}`).join(" ");
  const sent = pts((d) => d.sent);
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(max * f));
  const labelIdx = [0, Math.round((days.length - 1) / 4), Math.round((days.length - 1) / 2), Math.round((3 * (days.length - 1)) / 4), days.length - 1];
  const last = days[days.length - 1];
  const lx = x(days.length - 1), ly = y(last.sent);
  const tipX = Math.min(lx - 42, R - 84);
  return (
    <svg className="line-chart" viewBox={`0 0 ${W} ${H}`} role="img" aria-label={t("analytics.overTime")}>
      {ticks.slice(1).map((v) => <line key={v} className="grid-line" x1={L - 6} x2={R + 10} y1={y(v)} y2={y(v)} />)}
      <line className="axis" x1={L - 6} x2={R + 10} y1={BOT} y2={BOT} />
      {ticks.map((v) => <text key={`t${v}`} x={L - 14} y={y(v) + 4} textAnchor="end">{v.toLocaleString()}</text>)}
      {[...new Set(labelIdx)].map((i) => <text key={`d${i}`} x={x(i)} y={BOT + 24} textAnchor="middle">{shortDate(days[i].day)}</text>)}
      <path className="area" d={`M${sent.split(" ").join(" L")} L${R},${BOT} L${L},${BOT} Z`} />
      <polyline className="recv" points={pts((d) => d.received)} />
      <polyline className="sent" points={sent} />
      <circle className="dot" cx={lx} cy={ly} r="4.5" />
      <g className="tip">
        <rect x={tipX} y={Math.max(0, ly - 36)} width="84" height="26" rx="6" />
        <text x={tipX + 42} y={Math.max(0, ly - 36) + 17} textAnchor="middle">{t("analytics.sentCount", { count: last.sent })}</text>
      </g>
      <title>{days.map((d) => `${d.day}: ${d.sent} ${t("analytics.sent")}, ${d.received} ${t("analytics.received")}`).join("\n")}</title>
    </svg>
  );
}

function Breakdown({ title, sub, first, rows, total, label }: {
  title: string; sub: string; first: string;
  rows: (UsageCounts & { key: string })[];
  total: number;
  label: (k: string) => string;
}) {
  const { t } = useTranslation();
  const sorted = [...rows].sort((a, b) => b.sent - a.sent);
  return (
    <div className="card flush">
      <div className="chd"><div><h2>{title}</h2><p>{sub}</p></div></div>
      {sorted.length === 0 ? (
        <div className="cb muted">{t("analytics.none")}</div>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{first}</th>
                <th className="r">{t("analytics.messages")}</th>
                <th className="r">{t("analytics.share")}</th>
                <th className="r">{t("analytics.estCost")}</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((r) => (
                <tr key={r.key}>
                  <td><b style={{ color: "var(--text)" }}>{label(r.key)}</b></td>
                  <td className="r num-t">{r.sent.toLocaleString()}</td>
                  <td className="r num-t">{percent(r.sent, total)}</td>
                  <td className="r num-t">{rupees(r.est_cost_minor)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
