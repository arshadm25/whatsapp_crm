import { useTranslation } from "react-i18next";
import { NavLink } from "react-router-dom";
import Icon, { type IconName } from "./Icon";
import type { Bucket } from "../lib/devAnalytics";

// Shared pieces of the Developers pages (API keys, Webhooks, API logs).

export const DEV_RANGES = [1, 7, 30] as const;
export type DevRange = (typeof DEV_RANGES)[number];

export function RangePicker({ value, onChange, ranges = DEV_RANGES }: { value: number; onChange: (d: DevRange) => void; ranges?: readonly DevRange[] }) {
  const { t } = useTranslation();
  return (
    <div className="segmented" role="radiogroup" aria-label={t("analytics.range")}>
      {ranges.map((d) => (
        <button key={d} type="button" role="radio" aria-checked={d === value} className={d === value ? "on" : ""} onClick={() => onChange(d)}>
          {d === 1 ? t("devpages.last24h") : t("analytics.days", { count: d })}
        </button>
      ))}
    </div>
  );
}

export function Stat({ label, value, foot, icon, tone = "" }: { label: string; value: string; foot: string; icon: IconName; tone?: string }) {
  return (
    <div className="card stat">
      <div className="sh"><span className="sl">{label}</span><span className={`ic ${tone}`}><Icon name={icon} size="s" /></span></div>
      <span className="sv">{value}</span>
      <span className="sf">{foot}</span>
    </div>
  );
}

// The three Developers pages, as tabs under the page title.
export function DevTabs() {
  const { t } = useTranslation();
  const tabs = [
    { to: "/developers/keys", label: t("nav.apiKeys"), icon: "key" as IconName },
    { to: "/developers/webhooks", label: t("nav.webhooks"), icon: "zap" as IconName },
    { to: "/developers/logs", label: t("nav.apiLogs"), icon: "activity" as IconName },
  ];
  return (
    <nav className="segmented tabs dev-tabs" aria-label={t("nav.developers")}>
      {tabs.map((tab) => (
        <NavLink key={tab.to} to={tab.to} className={({ isActive }) => (isActive ? "on" : "")}>
          <Icon name={tab.icon} size="s" />{tab.label}
        </NavLink>
      ))}
    </nav>
  );
}

export type BarSeries = { key: string; label: string; color: string; values: number[] };

// Stacked bars per bucket, with a legend above. Each bucket's totals are in its tooltip.
export function StackedBars({ buckets, series, label }: { buckets: Bucket[]; series: BarSeries[]; label: string }) {
  const { t } = useTranslation();
  const W = 720, H = 220, L = 46, R = 710, TOP = 12, BOT = 186;
  const totals = buckets.map((_, i) => series.reduce((s, x) => s + (x.values[i] ?? 0), 0));
  const top = Math.max(...totals, 0);
  const max = top <= 4 ? 4 : niceCeil(top);
  const slot = (R - L) / Math.max(1, buckets.length);
  const bw = Math.max(2, Math.min(28, slot * 0.62));
  const y = (v: number) => BOT - (v / max) * (BOT - TOP);
  const ticks = [0, 0.5, 1].map((f) => Math.round(max * f));
  const every = Math.max(1, Math.ceil(buckets.length / 8));
  return (
    <>
      <div className="lg-row" style={{ marginBottom: 8 }}>
        {series.map((s) => <span key={s.key} className="lg"><i style={{ background: s.color }} />{s.label}</span>)}
      </div>
      {top === 0 ? (
        <div className="muted chart-empty">{t("devpages.noData")}</div>
      ) : (
        <svg className="line-chart" viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label}>
          {ticks.slice(1).map((v) => <line key={v} className="grid-line" x1={L - 6} x2={R} y1={y(v)} y2={y(v)} />)}
          <line className="axis" x1={L - 6} x2={R} y1={BOT} y2={BOT} />
          {ticks.map((v) => <text key={`t${v}`} x={L - 12} y={y(v) + 4} textAnchor="end">{v.toLocaleString()}</text>)}
          {buckets.map((b, i) => {
            const cx = L + slot * i + slot / 2;
            let acc = 0;
            return (
              <g key={b.key}>
                <title>{`${b.label}: ${series.map((s) => `${s.label} ${(s.values[i] ?? 0).toLocaleString()}`).join(", ")}`}</title>
                <rect x={cx - slot / 2} y={TOP} width={slot} height={BOT - TOP} fill="transparent" />
                {series.map((s) => {
                  const v = s.values[i] ?? 0;
                  if (!v) return null;
                  const y0 = y(acc), y1 = y(acc + v);
                  acc += v;
                  return <rect key={s.key} x={cx - bw / 2} y={y1} width={bw} height={Math.max(1, y0 - y1)} rx={2} fill={s.color} />;
                })}
                {i % every === 0 && <text x={cx} y={BOT + 20} textAnchor="middle">{b.label}</text>}
              </g>
            );
          })}
        </svg>
      )}
    </>
  );
}

function niceCeil(v: number) {
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

// A row of labelled share bars, largest first.
export function ShareRows({ rows, total, tone }: { rows: { key: string; label: string; n: number }[]; total: number; tone?: (key: string) => string }) {
  const { t } = useTranslation();
  if (!rows.length || !total) return <div className="muted small">{t("devpages.noData")}</div>;
  return (
    <>
      {rows.map((r) => {
        const p = Math.round((r.n * 100) / total);
        return (
          <div key={r.key} className="cat">
            {r.label}
            <div className="bar" style={{ height: 8 }}><span style={{ width: `${Math.max(p, r.n ? 1 : 0)}%`, background: tone?.(r.key) }} /></div>
            <b>{r.n.toLocaleString()}</b>
          </div>
        );
      })}
    </>
  );
}

// Who may use the Developers pages.
export function canUseDevelopers(role: string | undefined) {
  return role === "owner" || role === "admin" || role === "developer";
}
