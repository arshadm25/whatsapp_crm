import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type {
  AdminConversation, AdminInvoice, DeletionRequest, AdminListedTenant, AdminOverview, AdminTenant, AdminTenantDetail, MetaErrorGroup, AuditEntry, Message, MetaApiError, Page, Plan, WebhookHealth,
} from "../api/types";
import { formatPaise, rupeesToPaise } from "../lib/billing";
import { messageText } from "../lib/messages";
import { tierLabel } from "../lib/numbers";
import MetaFeesAdmin, { PaymentMode } from "./AdminMetaFees";
import AdminSettings, { MarkupCard } from "./AdminSettings";
import Icon, { type IconName } from "../components/Icon";
import ReasonField from "../components/ReasonField";
import AdminShell, { ADMIN_TABS, adminPath, type AdminTab } from "../components/AdminShell";
import { ago, initials } from "../lib/time";

function message(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

const when = (s: string | null) => (s ? new Date(s).toLocaleString() : "—");
const PILL: Record<AdminTenant["status"], string> = { active: "ok", suspended: "er", closed: "" };

function Quality({ q }: { q: AdminListedTenant["worst_quality"] }) {
  const { t } = useTranslation();
  if (!q) return <span className="muted">—</span>;
  return <span className={`pill ${q === "green" ? "ok" : q === "yellow" ? "wa" : "er"}`}>{t(`numbers.quality_${q}`)}</span>;
}

// usePaged walks an admin list by its next_cursor.
function usePaged<T>(key: string, path: string, params: URLSearchParams) {
  const q = useInfiniteQuery({
    queryKey: ["admin", key, params.toString()],
    queryFn: ({ pageParam }) =>
      api<Page<T>>("GET", `${path}?${params}${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  return { ...q, rows: q.data?.pages.flatMap((p) => p.data) ?? [] };
}

function More({ q }: { q: { hasNextPage: boolean; isFetchingNextPage: boolean; fetchNextPage: () => unknown } }) {
  const { t } = useTranslation();
  if (!q.hasNextPage) return null;
  return (
    <div className="actions">
      <button disabled={q.isFetchingNextPage} onClick={() => q.fetchNextPage()}>{t("common.loadMore")}</button>
    </div>
  );
}

type Tab = AdminTab;

export default function Admin() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const navigate = useNavigate();
  const params = useParams();
  const [search] = useSearchParams();
  const tab = (params.tab ?? "overview") as Tab;
  if (!me.user.is_platform_admin) return <Navigate to="/" replace />;
  if (!ADMIN_TABS.includes(tab)) return <Navigate to="/admin" replace />;
  const goTab = (k: Tab) => navigate(adminPath(k));
  if (!me.user.two_factor_enabled) {
    return (
      <AdminShell>
        <h1>{t("admin.title")}</h1>
        <div className="card notice">
          {t("admin.needs2fa")} <Link to="/settings">{t("admin.openSettings")}</Link>
        </div>
      </AdminShell>
    );
  }
  return (
    <AdminShell>
      <div className="page-head">
        <div>
          <h1>{tab === "overview" ? t("admin.title") : t(`admin.tab_${tab}`)}</h1>
          {tab === "overview" && <p className="sub">{t("admin.intro")}</p>}
          {tab === "settings" && <p className="sub">{t("admin.settingsIntro")}</p>}
        </div>
      </div>
      {tab === "overview" && <Overview goTab={goTab} openTenant={(id) => navigate(`/admin/tenants?open=${encodeURIComponent(id)}`)} />}
      {tab === "tenants" && <Tenants key={search.toString()} initial={search.get("open")} initialQuery={search.get("q") ?? ""} />}
      {tab === "plans" && <Plans />}
      {tab === "invoices" && <Invoices />}
      {tab === "metaFees" && <MetaFeesAdmin />}
      {tab === "webhooks" && <Webhooks />}
      {tab === "metaErrors" && <MetaErrors />}
      {tab === "deletions" && <Deletions />}
      {tab === "audit" && <Audit />}
      {tab === "settings" && <AdminSettings />}
    </AdminShell>
  );
}

// Overview is the staff landing page: platform-wide numbers, webhook throughput, the latest
// Meta API errors, the newest tenants and the latest audit entries.
function Overview({ goTab, openTenant }: { goTab: (t: Tab) => void; openTenant: (id: string) => void }) {
  const { t } = useTranslation();
  const [hours, setHours] = useState("24");
  const tenants = usePaged<AdminListedTenant>("tenants", "/internal/admin/tenants", new URLSearchParams({ limit: "50" }));
  // Counts across every workspace and error, not only the loaded pages.
  const ov = useQuery({
    queryKey: ["admin-overview", hours],
    queryFn: () => api<AdminOverview>("GET", `/internal/admin/overview?hours=${hours}`),
    refetchInterval: 60_000,
  });
  const summary = useQuery({
    queryKey: ["meta-error-groups", Number(hours)],
    queryFn: async () => (await api<{ data: MetaErrorGroup[] }>("GET", `/internal/admin/meta-errors/summary?hours=${hours}`)).data,
  });
  const audit = usePaged<AuditEntry>("audit", "/internal/admin/audit-log", new URLSearchParams({ limit: "50" }));
  const health = useQuery({
    queryKey: ["admin", "webhooks", hours],
    queryFn: () => api<WebhookHealth>("GET", `/internal/admin/webhook-health?hours=${hours}`),
    refetchInterval: 60_000,
  });
  const h = health.data;
  const received = h?.hours.reduce((a, x) => a + x.received, 0) ?? 0;
  const failed = h?.hours.reduce((a, x) => a + x.failed, 0) ?? 0;
  const perMin = h ? received / Math.max(1, h.hours.length * 60) : 0;
  const peak = h ? Math.max(0, ...h.hours.map((x) => x.received)) : 0;
  const peakHour = h?.hours.find((x) => x.received === peak)?.hour;
  const errorRate = received ? (failed * 100) / received : 0;
  const kpi = ov.data;
  const errDelta = kpi ? kpi.meta_errors.current - kpi.meta_errors.previous : 0;
  const groups = summary.data ?? [];

  return (
    <div className="stack">
      <div className="actions" style={{ justifyContent: "flex-end" }}>
        <div className="segmented" role="radiogroup" aria-label={t("admin.window")}>
          {[["1", "1h"], ["24", "24h"], ["168", "7d"]].map(([v, l]) => (
            <button key={v} className={hours === v ? "on" : ""} onClick={() => setHours(v)}>{l}</button>
          ))}
        </div>
      </div>
      <div className="grid g4">
        <div className="card stat">
          <div className="sh"><span className="sl">{t("admin.activeTenants")}</span><span className="ic"><Icon name="building" size="s" /></span></div>
          <span className="sv">{kpi ? kpi.tenants.active.toLocaleString() : "—"}</span>
          <span className="sf">{kpi ? t("admin.activeTenantsSub", { added: kpi.tenants.new_this_week, suspended: kpi.tenants.suspended }) : ""}</span>
        </div>
        <div className="card stat">
          <div className="sh"><span className="sl">{t("admin.eventsPerMin")}</span><span className="ic bl"><Icon name="activity" size="s" /></span></div>
          <span className="sv">{perMin.toFixed(1)}</span>
          <span className="sf">{peakHour ? t("admin.peakAt", { count: Math.round(peak / 60), time: new Date(peakHour).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) }) : "—"}</span>
        </div>
        <div className="card stat">
          <div className="sh"><span className="sl">{t("admin.errorRate")}</span><span className={errorRate > 1 ? "ic rd" : "ic"}><Icon name="shield" size="s" /></span></div>
          <span className="sv">{received ? `${errorRate.toFixed(2)}%` : "—"}</span>
          <span className="sf"><span className={`pill ${errorRate > 1 ? "er" : "ok"}`}>{errorRate > 1 ? t("admin.aboveTarget") : t("admin.withinTarget")}</span></span>
        </div>
        <div className="card stat">
          <div className="sh">
            <span className="sl">{t("admin.tab_metaErrors")}</span>
            {kpi && errDelta !== 0 && <span className={errDelta > 0 ? "dn" : "up"}>{errDelta > 0 ? `+${errDelta}` : errDelta}</span>}
            <span className={kpi?.meta_errors.current ? "ic am" : "ic gy"}><Icon name="alert" size="s" /></span>
          </div>
          <span className="sv">{kpi ? kpi.meta_errors.current.toLocaleString() : "—"}</span>
          <span className="sf">
            {t("admin.inWindow", { hours: Number(hours) })}
            {kpi && ` · ${errDelta === 0 ? t("admin.sameAsPrevious") : t(errDelta > 0 ? "admin.moreThanPrevious" : "admin.fewerThanPrevious", { count: Math.abs(errDelta) })}`}
          </span>
        </div>
      </div>
      <div className="split">
        {h && h.hours.length > 1 ? <Throughput hours={h.hours} /> : <div className="card muted">{t("admin.noWebhooks")}</div>}
        <div className="card flush">
          <div className="chd">
            <div><h2>{t("admin.recentMetaErrors")}</h2><p>{t("admin.errorsByCodeSub")}</p></div>
            <button className="link" onClick={() => goTab("metaErrors")}>{t("admin.all")}</button>
          </div>
          {groups.length === 0 && <div className="cb muted">{t("admin.noMetaErrors")}</div>}
          {groups.slice(0, 5).map((g) => (
            <div key={`${g.code}-${g.subcode}-${g.http_status}`} className="ev">
              <span className={`pill k ${g.http_status >= 500 ? "er" : "wa"}`}>{g.code ?? g.http_status}</span>
              <span className="t"><b>{g.message || `HTTP ${g.http_status}`}</b><br />{t("admin.errorCount", { count: g.count })} · {t("admin.errorTenants", { count: g.tenants })}</span>
              <span className="muted">{ago(g.last_at, t)}</span>
            </div>
          ))}
        </div>
      </div>
      <div className="card flush">
        <div className="chd">
          <div><h2>{t("admin.tab_tenants")}</h2><p>{t("admin.newestFirst")}</p></div>
          <button className="sm" onClick={() => goTab("tenants")}>{t("admin.allWorkspaces")}</button>
        </div>
        <div className="table-wrap">
          <table className="clickable">
            <thead>
              <tr>
                <th>{t("admin.workspace")}</th>
                <th>{t("admin.plan")}</th>
                <th className="r">{t("admin.numbers")}</th>
                <th className="r">{t("admin.messages30")}</th>
                <th>{t("admin.quality")}</th>
                <th>{t("admin.status")}</th>
                <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {tenants.rows.slice(0, 8).map((w) => (
                <tr key={w.id} onClick={() => openTenant(w.id)}>
                  <td><div className="who"><span className="av">{initials(w.name)}</span><span><b>{w.name}</b><small>{w.waba_id ? `WABA ${w.waba_id}` : w.slug}</small></span></div></td>
                  <td>{w.plan_code ?? (w.subscription_status === "trialing" ? t("admin.trial") : "—")}</td>
                  <td className="r num-t">{w.number_count}</td>
                  <td className="r num-t">{w.messages_30d.toLocaleString()}</td>
                  <td><Quality q={w.worst_quality} /></td>
                  <td><span className={`pill ${PILL[w.status]}`}>{t(`admin.status_${w.status}`)}</span></td>
                  <td className="r"><button className="sm">{t("admin.open")}</button></td>
                </tr>
              ))}
              {tenants.isSuccess && tenants.rows.length === 0 && <tr><td colSpan={7} className="muted">{t("admin.noTenants")}</td></tr>}
            </tbody>
          </table>
        </div>
      </div>
      <div className="card flush">
        <div className="chd">
          <div><h2>{t("admin.tab_audit")}</h2><p>{t("admin.auditSub")}</p></div>
          <button className="link" onClick={() => goTab("audit")}>{t("admin.viewAll")}</button>
        </div>
        {audit.rows.length === 0 && <div className="cb muted">{t("admin.noAudit")}</div>}
        {audit.rows.slice(0, 5).map((a) => (
          <div key={a.id} className="ev">
            <span className="av">{initials(a.actor_email ?? a.actor_type)}</span>
            <span className="t">
              <b>{a.actor_email ?? a.actor_type}</b> <code>{a.action}</code> <b>{a.target_type} {a.target_id}</b>
              {a.reason && <><br />{t("admin.reasonLine", { reason: a.reason })}</>}
            </span>
            <span className="muted">{ago(a.occurred_at, t)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function Tenants({ initial = null, initialQuery = "" }: { initial?: string | null; initialQuery?: string }) {
  const { t } = useTranslation();
  const [q, setQ] = useState(initialQuery);
  const [status, setStatus] = useState("");
  const [open, setOpen] = useState<string | null>(initial);
  const params = new URLSearchParams({ limit: "50" });
  if (q.trim()) params.set("q", q.trim());
  if (status) params.set("status", status);
  const list = usePaged<AdminListedTenant>("tenants", "/internal/admin/tenants", params);

  if (open) return <TenantPanel id={open} onBack={() => setOpen(null)} />;
  return (
    <>
      <div className="filters">
        <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("admin.searchTenants")} />
        <select value={status} onChange={(e) => setStatus(e.target.value)} aria-label={t("admin.status")}>
          <option value="">{t("admin.allStatuses")}</option>
          {["active", "suspended", "closed"].map((s) => <option key={s} value={s}>{t(`admin.status_${s}`)}</option>)}
        </select>
      </div>
      <div className="card table-wrap">
        <table className="clickable">
          <thead>
            <tr>
              <th>{t("admin.workspace")}</th>
              <th>{t("admin.waba")}</th>
              <th>{t("admin.plan")}</th>
              <th className="r">{t("admin.numbers")}</th>
              <th className="r">{t("admin.messages30")}</th>
              <th>{t("admin.quality")}</th>
              <th>{t("admin.status")}</th>
              <th>{t("admin.created")}</th>
            </tr>
          </thead>
          <tbody>
            {list.rows.map((w) => (
              <tr key={w.id} onClick={() => setOpen(w.id)}>
                <td><div className="who"><span className="av">{initials(w.name)}</span><span><b>{w.name}</b><small>{w.slug}</small></span></div></td>
                <td>
                  {w.waba_id ? <span className="k">{w.waba_id}</span> : <span className="muted">—</span>}
                  {w.waba_count > 1 && <span className="muted"> +{w.waba_count - 1}</span>}
                </td>
                <td>
                  {w.plan_code ?? (w.subscription_status === "trialing" ? t("admin.trial") : "—")}
                  {w.subscription_status && w.subscription_status !== "active" && w.subscription_status !== "trialing" && (
                    <small className="muted" style={{ display: "block" }}>{w.subscription_status}</small>
                  )}
                </td>
                <td className="r num-t">{w.number_count}</td>
                <td className="r num-t">{w.messages_30d.toLocaleString()}</td>
                <td>
                  <Quality q={w.worst_quality} />
                </td>
                <td><span className={`pill ${PILL[w.status]}`}>{t(`admin.status_${w.status}`)}</span></td>
                <td className="small">{new Date(w.created_at).toLocaleDateString()}</td>
              </tr>
            ))}
            {list.isSuccess && list.rows.length === 0 && (
              <tr><td colSpan={8} className="muted">{t("admin.noTenants")}</td></tr>
            )}
          </tbody>
        </table>
      </div>
      <More q={list} />
    </>
  );
}

type DetailTab = "overview" | "numbers" | "billing" | "conversations" | "account";
const SUB_PILL: Record<NonNullable<AdminTenantDetail["subscription"]>["status"], string> = {
  trialing: "wa", active: "ok", past_due: "er", cancelled: "",
};

// usePlans is the plan list shared by the workspace page's subscription card and plan form.
function usePlans() {
  return useQuery({
    queryKey: ["admin", "plans"],
    queryFn: async () => (await api<{ data: Plan[] }>("GET", "/internal/admin/plans")).data,
  });
}

// TenantPanel is one workspace in the console: a header with its key facts, then tabs for
// its numbers, subscription and staff actions, conversations and account status.
function TenantPanel({ id, onBack }: { id: string; onBack: () => void }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState<DetailTab>("overview");
  const detail = useQuery({
    queryKey: ["admin", "tenant", id],
    queryFn: () => api<AdminTenantDetail>("GET", `/internal/admin/tenants/${id}`),
  });
  const plans = usePlans();
  const d = detail.data;
  if (!d) return <div className="card muted">{t("common.loading")}</div>;

  const sub = d.subscription;
  const planName = sub?.plan_code ? (plans.data?.find((p) => p.code === sub.plan_code)?.name ?? sub.plan_code) : null;
  const live = d.numbers.filter((n) => n.status !== "disconnected").length;
  const tabs: { key: DetailTab; label: string; icon: IconName; count?: number }[] = [
    { key: "overview", label: t("admin.tab_overview"), icon: "grid" },
    { key: "numbers", label: t("admin.numbers"), icon: "phone", count: d.numbers.length },
    { key: "billing", label: t("admin.dtab_billing"), icon: "file" },
    { key: "conversations", label: t("admin.conversations"), icon: "message" },
    { key: "account", label: t("admin.dtab_account"), icon: "shield" },
  ];

  return (
    <>
      <p className="ws-back"><button className="link" onClick={onBack}>← {t("admin.allWorkspaces")}</button></p>
      <div className="card ws-detail-head">
        <span className="av lg">{initials(d.name)}</span>
        <div className="ws-detail-title">
          <h2>{d.name} <span className={`pill ${PILL[d.status]}`}>{t(`admin.status_${d.status}`)}</span></h2>
          <p className="muted small">
            <span className="k">{d.slug}</span> · {t("admin.createdOn", { date: new Date(d.created_at).toLocaleDateString() })} · {d.time_zone}
          </p>
        </div>
        <dl className="ws-facts">
          <div>
            <dt>{t("admin.plan")}</dt>
            <dd>{sub ? <>{planName} <span className={`pill ${SUB_PILL[sub.status]}`}>{t(`admin.sub_${sub.status}`)}</span></> : "—"}</dd>
          </div>
          <div>
            <dt>{t("admin.numbers")}</dt>
            <dd>{t("admin.numbersLive", { live, total: d.numbers.length })}</dd>
          </div>
          <div>
            <dt>{t("admin.metaPaymentMode")}</dt>
            <dd>{t(`admin.modeShort_${d.meta_payment_mode}`)}</dd>
          </div>
        </dl>
      </div>

      {d.suspended_reason && (
        <div className="banner danger"><Icon name="alert" size="s" /><div><span>{t("admin.suspendedFor", { reason: d.suspended_reason })}</span></div></div>
      )}
      {sub && !sub.usable && (
        <div className="banner danger"><Icon name="stop" size="s" /><div><b>{t("admin.sendingStoppedTitle")}</b><span>{t("admin.sendingStoppedHelp")}</span></div></div>
      )}

      <nav className="segmented tabs" aria-label={d.name}>
        {tabs.map((x) => (
          <button key={x.key} type="button" className={tab === x.key ? "on" : ""} aria-pressed={tab === x.key} onClick={() => setTab(x.key)}>
            <Icon name={x.icon} size="s" />{x.label}
            {x.count !== undefined && <span className="tab-count">{x.count}</span>}
          </button>
        ))}
      </nav>

      {tab === "overview" && <TenantOverview d={d} planName={planName} goNumbers={() => setTab("numbers")} />}
      {tab === "numbers" && <TenantNumbers numbers={d.numbers} />}
      {tab === "billing" && (
        <div className="grid g2 ws-actions">
          {sub ? <SubscriptionCard sub={sub} planName={planName} /> : <div className="card muted">{t("admin.noSubscription")}</div>}
          {sub?.status === "trialing" && <ExtendTrial key={`trial-${sub.current_period_end}`} id={id} sub={sub} />}
          {sub && <ChangePlan id={id} sub={sub} />}
          <PaymentMode key={d.meta_payment_mode} id={id} mode={d.meta_payment_mode} />
          <MarkupCard key={`markup-${d.meta_markup_bp}`} id={id} own={d.meta_markup_bp} payingThroughUs={d.meta_payment_mode === "through_us"} />
        </div>
      )}
      {tab === "conversations" && <Conversations tenantId={id} />}
      {tab === "account" && <AccountStatus key={d.status} id={id} status={d.status} />}
    </>
  );
}

function TenantOverview({ d, planName, goNumbers }: { d: AdminTenantDetail; planName: string | null; goNumbers: () => void }) {
  const { t } = useTranslation();
  const deliveries = Object.entries(d.webhook_deliveries_24h);
  const wabas = [...new Set(d.numbers.map((n) => n.waba_id))];
  return (
    <div className="stack">
      <dl className="stats">
        <div><dt>{t("admin.members")}</dt><dd>{d.members}</dd></div>
        <div><dt>{t("admin.sent30")}</dt><dd>{d.sent_30d.toLocaleString()}</dd></div>
        <div><dt>{t("admin.received30")}</dt><dd>{d.received_30d.toLocaleString()}</dd></div>
        <div><dt>{t("admin.lastMessage")}</dt><dd className="stat-text">{d.last_message_at ? <span title={when(d.last_message_at)}>{ago(d.last_message_at, t)}</span> : "—"}</dd></div>
      </dl>
      <div className="grid g2">
        {d.subscription ? <SubscriptionCard sub={d.subscription} planName={planName} /> : <div className="card muted">{t("admin.noSubscription")}</div>}
        <div className="card">
          <h2>{t("admin.details")}</h2>
          <dl className="kv">
            <dt>{t("admin.slug")}</dt><dd><span className="k">{d.slug}</span></dd>
            <dt>{t("admin.legalName")}</dt><dd>{d.legal_name || "—"}</dd>
            <dt>{t("admin.created")}</dt><dd>{new Date(d.created_at).toLocaleDateString()}</dd>
            <dt>{t("admin.timeZone")}</dt><dd>{d.time_zone}</dd>
            <dt>{t("admin.metaPaymentMode")}</dt><dd>{t(`admin.mode_${d.meta_payment_mode}`)}</dd>
            <dt>{t("admin.wabas")}</dt>
            <dd>{wabas.length ? wabas.map((w) => <span key={w} className="k ws-waba">{w}</span>) : "—"}</dd>
          </dl>
        </div>
      </div>
      <div className="grid g2">
        <div className="card">
          <div className="page-head">
            <h2>{t("admin.numbers")}</h2>
            {d.numbers.length > 0 && <button className="link" onClick={goNumbers}>{t("admin.viewAll")}</button>}
          </div>
          {d.numbers.length === 0 ? <p className="muted small">{t("admin.noNumbers")}</p> : (
            <ul className="ws-num-list">
              {d.numbers.slice(0, 4).map((n) => (
                <li key={n.id}>
                  <span><b>{n.display_phone_number}</b><small className="muted">{n.verified_name ?? "—"}</small></span>
                  <NumberStatus status={n.status} />
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="card">
          <h2>{t("admin.deliveries24")}</h2>
          {deliveries.length ? (
            <div className="ws-pills">
              {deliveries.map(([k, v]) => (
                <span key={k} className={`pill ${k === "failed" ? "er" : k === "delivered" ? "ok" : ""}`}>{k} · {v.toLocaleString()}</span>
              ))}
            </div>
          ) : <p className="muted small">{t("admin.noDeliveries")}</p>}
        </div>
      </div>
    </div>
  );
}

const NUMBER_PILL: Record<string, string> = { connected: "ok", pending: "wa", error: "er", revoked: "er", disconnected: "" };

function NumberStatus({ status }: { status: string }) {
  const { t } = useTranslation();
  return <span className={`pill ${NUMBER_PILL[status] ?? ""}`}>{t(`numbers.status_${status}`, status)}</span>;
}

function TenantNumbers({ numbers }: { numbers: AdminTenantDetail["numbers"] }) {
  const { t } = useTranslation();
  const quality = (q: string) => {
    const k = q.toLowerCase();
    const cls = k === "green" ? "ok" : k === "yellow" ? "wa" : k === "red" ? "er" : "";
    return <span className={`pill ${cls}`}>{t(`numbers.quality_${k}`, q)}</span>;
  };
  return (
    <div className="card table-wrap">
      <table>
        <thead>
          <tr>
            <th>{t("admin.number")}</th>
            <th>{t("admin.status")}</th>
            <th>{t("admin.quality")}</th>
            <th className="r">{t("admin.tier")}</th>
            <th>{t("admin.waba")}</th>
            <th>{t("admin.connection")}</th>
          </tr>
        </thead>
        <tbody>
          {numbers.map((n) => (
            <tr key={n.id}>
              <td><b>{n.display_phone_number}</b><div className="muted small">{n.verified_name ?? "—"}</div></td>
              <td><NumberStatus status={n.status} /></td>
              <td>{quality(n.quality_rating)}</td>
              <td className="r num-t">{tierLabel(n.messaging_limit_tier, t("numbers.unlimited"))}</td>
              <td><span className="k">{n.waba_id}</span></td>
              <td className="small">{n.coexistence ? t("admin.coexistence") : t("admin.cloudApi")}</td>
            </tr>
          ))}
          {numbers.length === 0 && <tr><td colSpan={6} className="muted">{t("admin.noNumbers")}</td></tr>}
        </tbody>
      </table>
    </div>
  );
}

function SubscriptionCard({ sub, planName }: { sub: NonNullable<AdminTenantDetail["subscription"]>; planName: string | null }) {
  const { t } = useTranslation();
  const end = new Date(sub.current_period_end);
  const days = Math.ceil((end.getTime() - Date.now()) / 86_400_000);
  const endLabel = sub.status === "trialing" ? t("admin.trialEnds") : sub.cancel_at_period_end ? t("admin.endsOn") : t("admin.periodEnds");
  return (
    <div className="card">
      <div className="page-head">
        <h2>{t("admin.subTitle")}</h2>
        <span className={`pill ${SUB_PILL[sub.status]}`}>{t(`admin.sub_${sub.status}`)}</span>
      </div>
      <dl className="kv">
        <dt>{t("admin.plan")}</dt>
        <dd>{planName ?? t("admin.noPlan")}{sub.admin_granted && <small className="muted ws-note">{t("admin.grantedPlan")}</small>}</dd>
        {sub.plan_code && <><dt>{t("admin.interval")}</dt><dd>{t(`admin.interval_${sub.billing_interval}`)}</dd></>}
        <dt>{endLabel}</dt>
        <dd>
          {end.toLocaleDateString()}
          <small className="muted ws-note">{days > 0 ? t("admin.daysLeft", { count: days }) : t("admin.ended")}</small>
        </dd>
        {sub.extra_seats > 0 && <><dt>{t("admin.extraSeats")}</dt><dd>{sub.extra_seats}</dd></>}
        <dt>{t("admin.sending")}</dt>
        <dd>{sub.usable ? <span className="pill ok">{t("admin.sendingOk")}</span> : <span className="pill er">{t("admin.sendingStopped")}</span>}</dd>
        {sub.payment_pending && <><dt>{t("admin.payment")}</dt><dd><span className="pill wa">{t("admin.paymentPending")}</span></dd></>}
      </dl>
    </div>
  );
}

function ExtendTrial({ id, sub }: { id: string; sub: NonNullable<AdminTenantDetail["subscription"]> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [days, setDays] = useState("7");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const n = Number(days);
  // Mirrors the server: the extension starts from the trial end, or today if it has passed.
  const from = Math.max(new Date(sub.current_period_end).getTime(), Date.now());
  const until = n >= 1 && n <= 90 ? new Date(from + n * 86_400_000).toLocaleDateString() : null;
  const extend = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("POST", `/internal/admin/tenants/${id}/extend-trial`, { days: Number(days), reason });
      setReason("");
      await qc.invalidateQueries({ queryKey: ["admin", "tenant", id] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  return (
    <form className="card form ws-action" onSubmit={extend}>
      <h3><Icon name="calendar" size="s" />{t("admin.extendTrial")}</h3>
      <p className="muted small">{t("admin.extendHelp")}</p>
      <label className="field">
        {t("admin.extendDays")}
        <input type="number" min={1} max={90} value={days} onChange={(e) => setDays(e.target.value)} required />
        {until && <span className="hint">{t("admin.newTrialEnd", { date: until })}</span>}
      </label>
      <ReasonField value={reason} onChange={setReason} />
      {error && <div className="field-error">{error}</div>}
      <div className="actions"><button className="primary">{t("admin.extendTrial")}</button></div>
    </form>
  );
}

function ChangePlan({ id, sub }: { id: string; sub: NonNullable<AdminTenantDetail["subscription"]> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [error, setError] = useState("");
  const [grant, setGrant] = useState({ plan: "", until: "", reason: "" });
  const [granted, setGranted] = useState(false);
  const plans = usePlans();
  // Staff can set the plan only of a workspace that does not pay Razorpay for it.
  const paying = !!sub.plan_code && !sub.admin_granted && sub.status === "active" && !sub.payment_pending &&
    !(plans.data?.find((p) => p.code === sub.plan_code)?.free ?? false);
  const changePlan = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setGranted(false);
    try {
      await api("POST", `/internal/admin/tenants/${id}/plan`, grant);
      setGrant({ plan: "", until: "", reason: "" });
      setGranted(true);
      await qc.invalidateQueries({ queryKey: ["admin", "tenant", id] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  if (paying) {
    return (
      <div className="card form ws-action">
        <h3><Icon name="lock" size="s" />{t("admin.changePlan")}</h3>
        <p className="muted small">{t("admin.planPaidRazorpay")}</p>
      </div>
    );
  }
  return (
    <form className="card form ws-action" onSubmit={changePlan}>
      <h3><Icon name="sparkles" size="s" />{t("admin.changePlan")}</h3>
      <p className="muted small">{t("admin.changePlanHelp")}</p>
      <div className="row">
        <label className="field">
          {t("admin.changePlanTo")}
          <select value={grant.plan} onChange={(e) => setGrant({ ...grant, plan: e.target.value })} required>
            <option value="">{t("admin.pickPlan")}</option>
            {plans.data?.map((p) => <option key={p.code} value={p.code}>{p.name}{p.is_active ? "" : ` (${t("admin.hiddenPlan")})`}</option>)}
          </select>
        </label>
        <label className="field">
          {t("admin.planUntil")}
          <input type="date" value={grant.until} onChange={(e) => setGrant({ ...grant, until: e.target.value })} />
        </label>
      </div>
      <ReasonField value={grant.reason} onChange={(reason) => setGrant({ ...grant, reason })} />
      {granted && <div className="banner ok"><Icon name="check" size="s" /><div><span>{t("admin.planChanged")}</span></div></div>}
      {error && <div className="field-error">{error}</div>}
      <div className="actions"><button className="primary">{t("admin.changePlan")}</button></div>
    </form>
  );
}

function AccountStatus({ id, status }: { id: string; status: AdminTenant["status"] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  if (status === "closed") return <div className="card muted">{t("admin.closedNote")}</div>;
  const active = status === "active";
  const act = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("POST", `/internal/admin/tenants/${id}/${active ? "suspend" : "reactivate"}`, { reason });
      setReason("");
      await qc.invalidateQueries({ queryKey: ["admin"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  return (
    <form className={`card form ws-action narrow${active ? " ws-danger" : ""}`} onSubmit={act}>
      <h3><Icon name={active ? "stop" : "check"} size="s" />{active ? t("admin.suspendTitle") : t("admin.reactivateTitle")}</h3>
      <p className="muted small">{active ? t("admin.suspendHelp") : t("admin.reactivateHelp")}</p>
      <ReasonField value={reason} onChange={setReason} label={active ? t("admin.suspendReason") : t("admin.reactivateReason")} />
      {error && <div className="field-error">{error}</div>}
      <div className="actions">
        <button className={active ? "danger" : "primary"}>{active ? t("admin.suspend") : t("admin.reactivate")}</button>
      </div>
    </form>
  );
}

function Conversations({ tenantId }: { tenantId: string }) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [reading, setReading] = useState<AdminConversation | null>(null);
  const params = new URLSearchParams({ limit: "25" });
  if (q.trim()) params.set("q", q.trim());
  const list = usePaged<AdminConversation>(`conversations-${tenantId}`, `/internal/admin/tenants/${tenantId}/conversations`, params);

  return (
    <>
      <h3>{t("admin.conversations")}</h3>
      <p className="muted small">{t("admin.conversationsHelp")}</p>
      <div className="filters">
        <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("admin.searchPhone")} />
      </div>
      <div className="card table-wrap">
        <table>
          <tbody>
            {list.rows.map((c) => (
              <tr key={c.id}>
                <td>+{c.contact_wa_id}</td>
                <td>{c.status}</td>
                <td className="small">{when(c.last_message_at)}</td>
                <td><button className="link" onClick={() => setReading(c)}>{t("admin.readMessages")}</button></td>
              </tr>
            ))}
            {list.isSuccess && list.rows.length === 0 && <tr><td className="muted">{t("admin.noConversations")}</td></tr>}
          </tbody>
        </table>
      </div>
      <More q={list} />
      {reading && <ReadMessages tenantId={tenantId} conversation={reading} onClose={() => setReading(null)} />}
    </>
  );
}

// ReadMessages shows message content only after the admin states why; the server audits it.
function ReadMessages({ tenantId, conversation, onClose }: { tenantId: string; conversation: AdminConversation; onClose: () => void }) {
  const { t } = useTranslation();
  const [reason, setReason] = useState("");
  const [messages, setMessages] = useState<Message[] | null>(null);
  const [error, setError] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      const res = await api<{ data: Message[] }>(
        "POST", `/internal/admin/tenants/${tenantId}/conversations/${conversation.id}/messages`, { reason });
      setMessages(res.data);
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };

  return (
    <div className="card">
      <div className="page-head">
        <h3>{t("admin.messagesWith", { phone: conversation.contact_wa_id })}</h3>
        <button className="link" onClick={onClose}>{t("admin.close")}</button>
      </div>
      {!messages ? (
        <form className="inline-form" onSubmit={submit}>
          <label className="field grow">
            {t("admin.viewReason")}
            <input value={reason} onChange={(e) => setReason(e.target.value)} required minLength={5} maxLength={500} />
          </label>
          <button className="primary">{t("admin.showMessages")}</button>
        </form>
      ) : (
        <ul className="admin-thread">
          {[...messages].reverse().map((m) => (
            <li key={m.id} className={m.direction}>
              <span className="muted small">{when(m.created_at)} · {m.status}</span>
              <div>{messageText(m)}</div>
            </li>
          ))}
          {messages.length === 0 && <li className="muted">{t("admin.noMessages")}</li>}
        </ul>
      )}
      {error && <div className="error">{error}</div>}
    </div>
  );
}

function Webhooks() {
  const { t } = useTranslation();
  const [hours, setHours] = useState("24");
  const health = useQuery({
    queryKey: ["admin", "webhooks", hours],
    queryFn: () => api<WebhookHealth>("GET", `/internal/admin/webhook-health?hours=${hours}`),
    refetchInterval: 60_000,
  });
  const h = health.data;
  const total = h?.hours.reduce(
    (a, x) => ({ received: a.received + x.received, failed: a.failed + x.failed, pending: a.pending + x.pending }),
    { received: 0, failed: 0, pending: 0 },
  );

  return (
    <>
      <div className="filters">
        <select value={hours} onChange={(e) => setHours(e.target.value)} aria-label={t("admin.window")}>
          {["1", "6", "24", "72", "168"].map((v) => <option key={v} value={v}>{t("admin.lastHours", { count: Number(v) })}</option>)}
        </select>
      </div>
      {total && h && (
        <dl className="stats">
          <div><dt>{t("admin.received")}</dt><dd>{total.received}</dd></div>
          <div><dt>{t("admin.perMinute")}</dt><dd>{(total.received / Math.max(1, h.hours.length * 60)).toFixed(1)}</dd></div>
          <div><dt>{t("admin.peakPerMinute")}</dt><dd>{(Math.max(0, ...h.hours.map((x) => x.received)) / 60).toFixed(1)}</dd></div>
          <div><dt>{t("admin.errorRate")}</dt><dd>{total.received ? `${((total.failed * 100) / total.received).toFixed(2)}%` : "—"}</dd></div>
          <div><dt>{t("admin.failed")}</dt><dd>{total.failed}</dd></div>
          <div><dt>{t("admin.pending")}</dt><dd>{total.pending}</dd></div>
        </dl>
      )}
      {h && h.hours.length > 1 && <Throughput hours={h.hours} />}
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("admin.hour")}</th>
              <th>{t("admin.received")}</th>
              <th>{t("admin.processed")}</th>
              <th>{t("admin.failed")}</th>
              <th>{t("admin.pending")}</th>
              <th>{t("admin.p95")}</th>
            </tr>
          </thead>
          <tbody>
            {h && [...h.hours].reverse().map((x) => (
              <tr key={x.hour} className={x.failed > 0 ? "warn" : ""}>
                <td className="small">{new Date(x.hour).toLocaleString()}</td>
                <td>{x.received}</td>
                <td>{x.processed}</td>
                <td>{x.failed}</td>
                <td>{x.pending}</td>
                <td>{x.p95_lag_seconds.toFixed(1)}s</td>
              </tr>
            ))}
            {h && h.hours.length === 0 && <tr><td colSpan={6} className="muted">{t("admin.noWebhooks")}</td></tr>}
          </tbody>
        </table>
      </div>
      {h && h.errors.length > 0 && (
        <>
          <h3>{t("admin.recentFailures")}</h3>
          <div className="card table-wrap">
            <table>
              <tbody>
                {h.errors.map((e) => (
                  <tr key={e.id}>
                    <td className="small">{when(e.received_at)}</td>
                    <td>{e.field ?? "—"}</td>
                    <td className="small muted">WABA {e.waba_id ?? "—"}</td>
                    <td className="small">{e.error}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}

// Invoices lists every GST invoice across workspaces for a date range; the CSV is for the accountant.
function Invoices() {
  const { t } = useTranslation();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const params = new URLSearchParams();
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  const qs = params.toString() ? `?${params}` : "";
  const list = useQuery({
    queryKey: ["admin", "invoices", from, to],
    queryFn: () => api<{ data: AdminInvoice[]; total_minor: number; truncated: boolean }>("GET", `/internal/admin/invoices${qs}`),
  });
  return (
    <>
      <SellerForm />
      <div className="filters">
        <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label={t("admin.from")} />
        <input type="date" value={to} onChange={(e) => setTo(e.target.value)} aria-label={t("admin.to")} />
        <a className="button end" href={`/internal/admin/invoices.csv${qs}`}>{t("admin.downloadCsv")}</a>
      </div>
      {list.data && <p className="muted small">{t("admin.invoiceTotal", { count: list.data.data.length, total: formatPaise(list.data.total_minor) })}{list.data.truncated ? ` ${t("admin.invoiceTruncated")}` : ""}</p>}
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("admin.invoiceNumber")}</th>
              <th>{t("admin.workspace")}</th>
              <th>{t("admin.invoiceBuyer")}</th>
              <th>{t("admin.invoiceTax")}</th>
              <th>{t("admin.invoiceTotalCol")}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {list.data?.data.map((i) => (
              <tr key={i.id}>
                <td>{i.number}<div className="small muted">{when(i.issued_at)}</div></td>
                <td>{i.tenant_name}</td>
                <td>{i.buyer_name}<div className="small muted">{i.buyer_gstin || "—"}</div></td>
                <td className="small">
                  {i.igst_minor > 0 ? `IGST ${formatPaise(i.igst_minor)}` : `CGST ${formatPaise(i.cgst_minor)} · SGST ${formatPaise(i.sgst_minor)}`}
                </td>
                <td>{formatPaise(i.total_minor)}{!i.emailed && <div className="small muted">{t("admin.notEmailed")}</div>}</td>
                <td><a href={`/internal/admin/invoices/${i.id}/view`} target="_blank" rel="noreferrer">{t("admin.viewInvoice")}</a></td>
              </tr>
            ))}
            {list.isSuccess && list.data.data.length === 0 && <tr><td colSpan={6} className="muted">{t("admin.noInvoices")}</td></tr>}
          </tbody>
        </table>
      </div>
    </>
  );
}

type Seller = {
  name: string; gstin: string; address: string; sac: string; gst_rate_bp: number;
  source: "console" | "settings"; updated_at: string | null; invoicing: boolean;
};

// SellerForm edits the seller printed on GST invoices. Invoicing is on while a GSTIN is saved.
function SellerForm() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const seller = useQuery({ queryKey: ["admin", "seller"], queryFn: () => api<Seller>("GET", "/internal/admin/seller") });
  const [form, setForm] = useState<{ name: string; gstin: string; address: string; sac: string } | null>(null);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  if (!seller.data) return null;
  const f = form ?? { name: seller.data.name, gstin: seller.data.gstin, address: seller.data.address, sac: seller.data.sac };
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    setSaved(false);
    setForm({ ...f, [k]: e.target.value });
  };
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      const out = await api<Seller>("PUT", "/internal/admin/seller", f);
      qc.setQueryData(["admin", "seller"], out);
      setForm(null);
      setSaved(true);
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  return (
    <form className="card form" onSubmit={save}>
      <h2>{t("admin.sellerTitle")}</h2>
      <p className="muted small">{t("admin.sellerHelp")}</p>
      <p className="small">
        <span className={seller.data.invoicing ? "pill" : "pill danger-text"}>{seller.data.invoicing ? t("admin.sellerOn") : t("admin.sellerOff")}</span>{" "}
        {seller.data.source === "settings" && <span className="muted">{t("admin.sellerFromSettings")}</span>}
      </p>
      <div className="plan-form">
        <label className="field">{t("admin.sellerName")}<input value={f.name} onChange={set("name")} required maxLength={200} /></label>
        <label className="field">{t("admin.sellerGstin")}<input value={f.gstin} onChange={set("gstin")} maxLength={15} placeholder="32ABCDE1234F1Z5" autoCapitalize="characters" /></label>
        <label className="field">{t("admin.sellerSac")}<input value={f.sac} onChange={set("sac")} inputMode="numeric" pattern="[0-9]{6}" maxLength={6} /></label>
      </div>
      <label className="field">{t("admin.sellerAddress")}<textarea rows={2} value={f.address} onChange={set("address")} maxLength={500} required={f.gstin.trim() !== ""} /></label>
      <p className="muted small">{t("admin.sellerGstRateAdmin", { rate: seller.data.gst_rate_bp / 100 })} <Link to="/admin/settings">{t("admin.tab_settings")}</Link></p>
      {error && <div className="error">{error}</div>}
      {saved && <div className="muted small">{t("admin.sellerSaved")}</div>}
      <div className="actions">
        <button className="primary" disabled={form === null}>{t("admin.sellerSave")}</button>
      </div>
    </form>
  );
}

// Throughput draws events received per hour, with the failed share on top in red.
function Throughput({ hours }: { hours: WebhookHealth["hours"] }) {
  const { t } = useTranslation();
  const rows = [...hours].sort((a, b) => a.hour.localeCompare(b.hour));
  const max = Math.max(1, ...rows.map((x) => x.received));
  const w = 720;
  const hgt = 180;
  const bw = w / rows.length;
  const hm = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return (
    <div className="card flush">
      <div className="chd">
        <div><h2>{t("admin.throughput")}</h2><p>{t("admin.throughputSub", { count: rows.length })}</p></div>
        <div className="lg-row">
          <span className="lg"><i style={{ background: "var(--accent)" }} />{t("admin.received")}</span>
          <span className="lg"><i style={{ background: "var(--danger)" }} />{t("admin.failed")}</span>
        </div>
      </div>
      <div className="cb">
        <svg className="chart" style={{ height: 180 }} viewBox={`0 0 ${w} ${hgt + 24}`} preserveAspectRatio="none" role="img" aria-label={t("admin.throughput")}>
          {rows.map((x, i) => {
            const bh = (x.received / max) * (hgt - 4);
            const fh = (x.failed / max) * (hgt - 4);
            return (
              <g key={x.hour}>
                <title>{`${new Date(x.hour).toLocaleString()}: ${x.received} / ${x.failed}`}</title>
                <rect className="bar-sent" x={i * bw + 2} y={hgt - bh} width={Math.max(1, bw - 4)} height={bh} rx="3" />
                {x.failed > 0 && <rect className="bar-failed" x={i * bw + 2} y={hgt - fh} width={Math.max(1, bw - 4)} height={Math.max(2, fh)} rx="3" />}
              </g>
            );
          })}
          <line className="axis" x1="0" x2={w} y1={hgt} y2={hgt} />
        </svg>
        <div className="ticks small muted">
          <span>{hm(rows[0].hour)}</span>
          <span>{hm(rows[Math.floor(rows.length / 2)].hour)}</span>
          <span>{t("admin.now")}</span>
        </div>
      </div>
    </div>
  );
}

function MetaErrors() {
  const { t } = useTranslation();
  const [hours, setHours] = useState(24);
  const [code, setCode] = useState<number | null>(null);
  const params = new URLSearchParams({ limit: "50", hours: String(hours) });
  if (code != null) params.set("code", String(code));
  const list = usePaged<MetaApiError>(`meta-errors-${hours}-${code ?? ""}`, "/internal/admin/meta-errors", params);
  const groups = useQuery({
    queryKey: ["meta-error-groups", hours],
    queryFn: async () => (await api<{ data: MetaErrorGroup[] }>("GET", `/internal/admin/meta-errors/summary?hours=${hours}`)).data,
  });
  return (
    <>
      <div className="filters">
        <div className="segmented">
          {[24, 72, 168].map((h) => (
            <button key={h} type="button" className={hours === h ? "on" : ""} onClick={() => setHours(h)}>{t("admin.lastHours", { count: h })}</button>
          ))}
        </div>
        {code != null && <button type="button" className="link" onClick={() => setCode(null)}>{t("admin.clearCode", { code })}</button>}
      </div>
      {!!groups.data?.length && (
        <div className="card flush">
          <div className="chd"><div><h2>{t("admin.errorsByCode")}</h2><p>{t("admin.errorsByCodeWindow", { count: hours })}</p></div></div>
          {groups.data.map((g) => (
            <button
              key={`${g.code}-${g.subcode}-${g.http_status}`}
              type="button"
              className={`ql${code != null && code === g.code ? " on" : ""}`}
              onClick={() => g.code != null && setCode(g.code)}
            >
              <span className="chip muted"><code>{g.code ?? g.http_status}{g.subcode ? `/${g.subcode}` : ""}</code></span>
              <span className="t">
                <b>{g.message || "—"}</b>
                <small>{t("admin.errorCount", { count: g.count })} · {t("admin.errorTenants", { count: g.tenants })} · {when(g.last_at)}</small>
              </span>
            </button>
          ))}
        </div>
      )}
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("admin.when")}</th>
              <th>{t("admin.call")}</th>
              <th>{t("admin.error")}</th>
              <th>{t("admin.trace")}</th>
            </tr>
          </thead>
          <tbody>
            {list.rows.map((e) => (
              <tr key={e.id}>
                <td className="small">{when(e.occurred_at)}</td>
                <td className="small"><code>{e.method} {e.path}</code></td>
                <td>
                  {e.http_status} · {e.code ?? "—"}{e.subcode ? `/${e.subcode}` : ""}
                  <div className="small muted">{e.message}</div>
                </td>
                <td className="small muted">{e.fbtrace_id}</td>
              </tr>
            ))}
            {list.isSuccess && list.rows.length === 0 && <tr><td colSpan={4} className="muted">{t("admin.noMetaErrors")}</td></tr>}
          </tbody>
        </table>
      </div>
      <More q={list} />
    </>
  );
}

function Deletions() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [error, setError] = useState("");
  const list = useQuery({
    queryKey: ["admin", "deletions"],
    queryFn: async () => (await api<{ data: DeletionRequest[] }>("GET", "/internal/admin/deletion-requests")).data,
  });
  const mark = async (d: DeletionRequest, status: "in_progress" | "completed") => {
    setError("");
    try {
      await api("POST", `/internal/admin/deletion-requests/${d.id}/status`, { status });
      await qc.invalidateQueries({ queryKey: ["admin", "deletions"] });
    } catch (e) {
      setError(message(e, t("common.error")));
    }
  };
  return (
    <>
      <p className="muted small">{t("admin.deletionsIntro")}</p>
      {error && <div className="error">{error}</div>}
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("admin.when")}</th>
              <th>{t("admin.deletionCode")}</th>
              <th>{t("admin.deletionUser")}</th>
              <th>{t("admin.deletionStatus")}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {list.data?.map((d) => (
              <tr key={d.id}>
                <td className="small">{when(d.requested_at)}</td>
                <td><code>{d.confirmation_code}</code></td>
                <td className="small">{d.meta_user_id}</td>
                <td>{t(`admin.deletion_${d.status}`)}</td>
                <td>
                  {d.status === "received" && <button className="link" onClick={() => mark(d, "in_progress")}>{t("admin.deletionStart")}</button>}{" "}
                  {d.status !== "completed" && <button className="link" onClick={() => mark(d, "completed")}>{t("admin.deletionDone")}</button>}
                </td>
              </tr>
            ))}
            {list.isSuccess && list.data.length === 0 && <tr><td colSpan={5} className="muted">{t("admin.noDeletions")}</td></tr>}
          </tbody>
        </table>
      </div>
    </>
  );
}

function Audit() {
  const { t } = useTranslation();
  const [actor, setActor] = useState("");
  const params = new URLSearchParams({ limit: "50" });
  if (actor) params.set("actor_type", actor);
  const list = usePaged<AuditEntry>("audit", "/internal/admin/audit-log", params);
  return (
    <>
      <div className="filters">
        <select value={actor} onChange={(e) => setActor(e.target.value)} aria-label={t("admin.actor")}>
          <option value="">{t("admin.allActors")}</option>
          {["user", "platform_admin", "api_key", "system", "meta"].map((a) => <option key={a} value={a}>{a}</option>)}
        </select>
        <a className="button end" href={`/internal/admin/audit-log.csv${actor ? `?actor_type=${actor}` : ""}`}>{t("admin.downloadCsv")}</a>
      </div>
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("admin.when")}</th>
              <th>{t("admin.actor")}</th>
              <th>{t("admin.action")}</th>
              <th>{t("admin.target")}</th>
              <th>{t("admin.reason")}</th>
            </tr>
          </thead>
          <tbody>
            {list.rows.map((a) => (
              <tr key={a.id}>
                <td className="small">{when(a.occurred_at)}</td>
                <td className="small">{a.actor_email ?? a.actor_type}<div className="muted">{a.ip}</div></td>
                <td><code>{a.action}</code></td>
                <td className="small muted">{a.target_type} {a.target_id}</td>
                <td className="small">{a.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <More q={list} />
    </>
  );
}

const blankPlan = {
  code: "", name: "", price: "", seat: "", numbers: "1", seats: "3", ai: "0", razorpay: "", seatPlan: "", order: "0", active: true,
  contacts: "", messages: "", yearly: "", yearlyPlan: "", tagline: "", highlights: "", popular: false,
};

// Plans are set here because prices are decided by Ecogo, not fixed in code. Each plan also
// needs a Razorpay plan with the same monthly amount, created in the Razorpay dashboard.
function Plans() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const plans = useQuery({
    queryKey: ["admin", "plans"],
    queryFn: async () => (await api<{ data: Plan[] }>("GET", "/internal/admin/plans")).data,
  });
  const [form, setForm] = useState<typeof blankPlan | null>(null);
  const [error, setError] = useState("");
  const edit = (p: Plan) =>
    setForm({
      code: p.code, name: p.name, price: String(p.price_minor / 100), seat: String(p.extra_seat_minor / 100),
      numbers: String(p.included_numbers), seats: String(p.included_seats), ai: String(p.ai_replies_per_month ?? 0), razorpay: p.razorpay_plan_id ?? "", seatPlan: p.extra_seat_razorpay_plan_id ?? "",
      order: String(p.sort_order), active: p.is_active,
      contacts: p.contacts_limit === null ? "" : String(p.contacts_limit), messages: p.messages_per_month === null ? "" : String(p.messages_per_month),
      yearly: p.yearly_price_minor === null ? "" : String(p.yearly_price_minor / 100), yearlyPlan: p.yearly_razorpay_plan_id ?? "",
      tagline: p.tagline, highlights: p.highlights.join("\n"), popular: p.is_popular,
    });
  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!form) return;
    setError("");
    const price = rupeesToPaise(form.price), seat = rupeesToPaise(form.seat || "0");
    const yearly = form.yearly.trim() === "" ? null : rupeesToPaise(form.yearly);
    if (price === null || seat === null || (form.yearly.trim() !== "" && yearly === null)) {
      setError(t("admin.badPrice"));
      return;
    }
    try {
      await api("PUT", `/internal/admin/plans/${encodeURIComponent(form.code.trim())}`, {
        name: form.name, price_minor: price, extra_seat_minor: seat, included_numbers: Number(form.numbers),
        included_seats: Number(form.seats), ai_replies_per_month: Number(form.ai), razorpay_plan_id: form.razorpay.trim() || null,
        extra_seat_razorpay_plan_id: form.seatPlan.trim() || null, sort_order: Number(form.order),
        is_active: form.active,
        contacts_limit: form.contacts.trim() === "" ? null : Number(form.contacts),
        messages_per_month: form.messages.trim() === "" ? null : Number(form.messages),
        yearly_price_minor: yearly, yearly_razorpay_plan_id: form.yearlyPlan.trim() || null,
        tagline: form.tagline, highlights: form.highlights.split("\n"), is_popular: form.popular,
      });
      setForm(null);
      await qc.invalidateQueries({ queryKey: ["admin", "plans"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const set = (k: keyof typeof blankPlan) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setForm({ ...form!, [k]: k === "active" || k === "popular" ? (e.target as HTMLInputElement).checked : e.target.value });

  return (
    <>
      <div className="toolbar">
        <p className="muted small">{t("admin.plansHelp")}</p>
        {!form && <button className="primary" onClick={() => setForm(blankPlan)}>{t("admin.newPlan")}</button>}
      </div>
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("admin.plan")}</th>
              <th>{t("admin.price")}</th>
              <th>{t("admin.includes")}</th>
              <th>{t("admin.razorpayPlan")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {plans.data?.map((p) => (
              <tr key={p.code}>
                <td>{p.name} <span className="muted small">{p.code}</span> {!p.is_active && <span className="pill">{t("admin.hidden")}</span>}</td>
                <td>
                  {formatPaise(p.price_minor)}
                  {p.yearly_price_minor !== null && <div className="muted small">{t("admin.yearlyValue", { price: formatPaise(p.yearly_price_minor) })}</div>}
                  <div className="muted small">{t("admin.extraSeat", { price: formatPaise(p.extra_seat_minor) })}</div>
                </td>
                <td className="small">
                  {t("admin.includesValue", { numbers: p.included_numbers, seats: p.included_seats })} · {t("admin.aiRepliesValue", { count: p.ai_replies_per_month ?? 0 })}
                  {(p.contacts_limit !== null || p.messages_per_month !== null) && (
                    <div className="muted">{t("admin.limitsValue", { contacts: p.contacts_limit ?? "∞", messages: p.messages_per_month ?? "∞" })}</div>
                  )}
                </td>
                <td className="small">
                  {p.free ? "—" : p.razorpay_plan_id ?? <span className="danger-text">{t("admin.notPayable")}</span>}
                  {p.yearly_razorpay_plan_id && <div className="muted">{p.yearly_razorpay_plan_id}</div>}
                </td>
                <td><button className="link" onClick={() => edit(p)}>{t("admin.edit")}</button></td>
              </tr>
            ))}
            {plans.data?.length === 0 && <tr><td colSpan={5} className="muted">{t("admin.noPlans")}</td></tr>}
          </tbody>
        </table>
      </div>
      {form && (
        <form className="card form" onSubmit={save}>
          <div className="plan-form">
            <label className="field">{t("admin.planCode")}<input value={form.code} onChange={set("code")} required pattern="[a-z][a-z0-9_]{1,31}" /></label>
            <label className="field">{t("admin.planName")}<input value={form.name} onChange={set("name")} required maxLength={60} /></label>
            <label className="field">{t("admin.pricePerMonth")}<input inputMode="decimal" value={form.price} onChange={set("price")} required /></label>
            <label className="field">{t("admin.extraSeatPrice")}<input inputMode="decimal" value={form.seat} onChange={set("seat")} /></label>
            <label className="field">{t("admin.includedNumbers")}<input type="number" min={1} value={form.numbers} onChange={set("numbers")} required /></label>
            <label className="field">{t("admin.includedSeats")}<input type="number" min={1} value={form.seats} onChange={set("seats")} required /></label>
            <label className="field">{t("admin.aiReplies")}<input type="number" min={0} value={form.ai} onChange={set("ai")} required /></label>
            <label className="field">{t("admin.razorpayPlan")}<input value={form.razorpay} onChange={set("razorpay")} placeholder="plan_…" /></label>
            <label className="field">{t("admin.seatRazorpayPlan")}<input value={form.seatPlan} onChange={set("seatPlan")} placeholder="plan_…" /></label>
            <label className="field">{t("admin.contactsLimit")}<input type="number" min={0} value={form.contacts} onChange={set("contacts")} placeholder={t("admin.emptyNoLimit")} /></label>
            <label className="field">{t("admin.messagesLimit")}<input type="number" min={0} value={form.messages} onChange={set("messages")} placeholder={t("admin.emptyNoLimit")} /></label>
            <label className="field">{t("admin.yearlyPrice")}<input inputMode="decimal" value={form.yearly} onChange={set("yearly")} /></label>
            <label className="field">{t("admin.yearlyRazorpayPlan")}<input value={form.yearlyPlan} onChange={set("yearlyPlan")} placeholder="plan_…" /></label>
            <label className="field">{t("admin.sortOrder")}<input type="number" value={form.order} onChange={set("order")} /></label>
            <label className="field check"><input type="checkbox" checked={form.active} onChange={set("active")} /> {t("admin.showToCustomers")}</label>
            <label className="field check"><input type="checkbox" checked={form.popular} onChange={set("popular")} /> {t("admin.popular")}</label>
          </div>
          <label className="field">{t("admin.tagline")}<input value={form.tagline} onChange={set("tagline")} maxLength={200} /></label>
          <label className="field">{t("admin.highlights")}<textarea rows={5} value={form.highlights} onChange={set("highlights")} /></label>
          {error && <div className="error">{error}</div>}
          <div className="actions">
            <button type="button" onClick={() => setForm(null)}>{t("common.cancel")}</button>
            <button className="primary">{t("admin.savePlan")}</button>
          </div>
        </form>
      )}
    </>
  );
}
