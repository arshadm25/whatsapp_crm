import { useState, type FormEvent } from "react";
import { Link, Navigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type {
  AdminConversation, AdminInvoice, DeletionRequest, AdminTenant, AdminTenantDetail, AuditEntry, Message, MetaApiError, Page, Plan, WebhookHealth,
} from "../api/types";
import { formatPaise, rupeesToPaise } from "../lib/billing";
import { messageText } from "../lib/messages";
import MetaFeesAdmin, { PaymentMode } from "./AdminMetaFees";
import Icon from "../components/Icon";
import { ago, initials } from "../lib/time";

function message(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

const when = (s: string | null) => (s ? new Date(s).toLocaleString() : "—");
const PILL: Record<AdminTenant["status"], string> = { active: "ok", suspended: "er", closed: "" };

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

type Tab = "overview" | "tenants" | "plans" | "invoices" | "metaFees" | "webhooks" | "metaErrors" | "deletions" | "audit";
const TABS: Tab[] = ["overview", "tenants", "plans", "invoices", "metaFees", "webhooks", "metaErrors", "deletions", "audit"];

export default function Admin() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const [tab, setTab] = useState<Tab>("overview");
  const [openTenant, setOpenTenant] = useState<string | null>(null);
  if (!me.user.is_platform_admin) return <Navigate to="/" replace />;
  if (!me.user.two_factor_enabled) {
    return (
      <section>
        <h1>{t("admin.title")}</h1>
        <div className="card notice">
          {t("admin.needs2fa")} <Link to="/settings">{t("admin.openSettings")}</Link>
        </div>
      </section>
    );
  }
  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("admin.title")}</h1>
          <p className="sub">{t("admin.intro")}</p>
        </div>
      </div>
      <div className="segmented tabs">
        {TABS.map((k) => (
          <button key={k} className={tab === k ? "on" : ""} onClick={() => { setTab(k); setOpenTenant(null); }}>{t(`admin.tab_${k}`)}</button>
        ))}
      </div>
      {tab === "overview" && <Overview goTab={setTab} openTenant={(id) => { setOpenTenant(id); setTab("tenants"); }} />}
      {tab === "tenants" && <Tenants initial={openTenant} />}
      {tab === "plans" && <Plans />}
      {tab === "invoices" && <Invoices />}
      {tab === "metaFees" && <MetaFeesAdmin />}
      {tab === "webhooks" && <Webhooks />}
      {tab === "metaErrors" && <MetaErrors />}
      {tab === "deletions" && <Deletions />}
      {tab === "audit" && <Audit />}
    </section>
  );
}

// Overview is the staff landing page: platform-wide numbers, webhook throughput, the latest
// Meta API errors, the newest tenants and the latest audit entries.
function Overview({ goTab, openTenant }: { goTab: (t: Tab) => void; openTenant: (id: string) => void }) {
  const { t } = useTranslation();
  const [hours, setHours] = useState("24");
  const tenants = usePaged<AdminTenant>("tenants", "/internal/admin/tenants", new URLSearchParams({ limit: "50" }));
  const errors = usePaged<MetaApiError>("meta-errors", "/internal/admin/meta-errors", new URLSearchParams({ limit: "50" }));
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
  const since = new Date(Date.now() - Number(hours) * 3600_000).toISOString();
  const recentErrors = errors.rows.filter((e) => e.occurred_at >= since);
  const active = tenants.rows.filter((w) => w.status === "active").length;
  const suspended = tenants.rows.filter((w) => w.status === "suspended").length;
  const groups = Object.values(
    recentErrors.reduce<Record<string, { code: string; n: number; message: string; last: string; status: number }>>((acc, e) => {
      const code = String(e.code ?? e.http_status);
      const g = acc[code] ?? { code, n: 0, message: e.message ?? "", last: e.occurred_at, status: e.http_status };
      acc[code] = { ...g, n: g.n + 1, last: g.last > e.occurred_at ? g.last : e.occurred_at };
      return acc;
    }, {}),
  ).sort((a, b) => b.n - a.n);

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
          <span className="sv">{active}{tenants.hasNextPage ? "+" : ""}</span>
          <span className="sf">{t("admin.suspendedCount", { count: suspended })}</span>
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
          <div className="sh"><span className="sl">{t("admin.tab_metaErrors")}</span><span className={recentErrors.length ? "ic am" : "ic gy"}><Icon name="alert" size="s" /></span></div>
          <span className="sv">{recentErrors.length}{errors.hasNextPage && recentErrors.length === errors.rows.length ? "+" : ""}</span>
          <span className="sf">{t("admin.inWindow", { hours: Number(hours) })}</span>
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
            <div key={g.code} className="ev">
              <span className={`pill k ${g.status >= 500 ? "er" : "wa"}`}>{g.code}</span>
              <span className="t"><b>{g.message || `HTTP ${g.status}`}</b><br />{t("admin.errorCount", { count: g.n })}</span>
              <span className="muted">{ago(g.last, t)}</span>
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
                <th>{t("admin.status")}</th>
                <th>{t("admin.created")}</th>
                <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {tenants.rows.slice(0, 8).map((w) => (
                <tr key={w.id} onClick={() => openTenant(w.id)}>
                  <td><div className="who"><span className="av">{initials(w.name)}</span><span><b>{w.name}</b><small>{w.slug}</small></span></div></td>
                  <td><span className={`pill ${PILL[w.status]}`}>{t(`admin.status_${w.status}`)}</span></td>
                  <td>{new Date(w.created_at).toLocaleDateString()}</td>
                  <td className="r"><button className="sm">{t("admin.open")}</button></td>
                </tr>
              ))}
              {tenants.isSuccess && tenants.rows.length === 0 && <tr><td colSpan={4} className="muted">{t("admin.noTenants")}</td></tr>}
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

function Tenants({ initial = null }: { initial?: string | null }) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [open, setOpen] = useState<string | null>(initial);
  const params = new URLSearchParams({ limit: "50" });
  if (q.trim()) params.set("q", q.trim());
  if (status) params.set("status", status);
  const list = usePaged<AdminTenant>("tenants", "/internal/admin/tenants", params);

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
              <th>{t("admin.status")}</th>
              <th>{t("admin.created")}</th>
            </tr>
          </thead>
          <tbody>
            {list.rows.map((w) => (
              <tr key={w.id} onClick={() => setOpen(w.id)}>
                <td><div className="who"><span className="av">{initials(w.name)}</span><span><b>{w.name}</b><small>{w.slug}</small></span></div></td>
                <td><span className={`pill ${PILL[w.status]}`}>{t(`admin.status_${w.status}`)}</span></td>
                <td className="small">{new Date(w.created_at).toLocaleDateString()}</td>
              </tr>
            ))}
            {list.isSuccess && list.rows.length === 0 && (
              <tr><td colSpan={3} className="muted">{t("admin.noTenants")}</td></tr>
            )}
          </tbody>
        </table>
      </div>
      <More q={list} />
    </>
  );
}

function TenantPanel({ id, onBack }: { id: string; onBack: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const detail = useQuery({
    queryKey: ["admin", "tenant", id],
    queryFn: () => api<AdminTenantDetail>("GET", `/internal/admin/tenants/${id}`),
  });
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const d = detail.data;
  if (!d) return <div className="card muted">{t("common.loading")}</div>;

  const act = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("POST", `/internal/admin/tenants/${id}/${d.status === "active" ? "suspend" : "reactivate"}`, { reason });
      setReason("");
      await qc.invalidateQueries({ queryKey: ["admin"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const deliveries = Object.entries(d.webhook_deliveries_24h);

  return (
    <>
      <p><button className="link" onClick={onBack}>← {t("admin.allWorkspaces")}</button></p>
      <div className="page-head">
        <h2>{d.name} <span className={`pill ${d.status}`}>{t(`admin.status_${d.status}`)}</span></h2>
      </div>
      {d.suspended_reason && <div className="card notice">{t("admin.suspendedFor", { reason: d.suspended_reason })}</div>}
      <dl className="stats">
        <div><dt>{t("admin.members")}</dt><dd>{d.members}</dd></div>
        <div><dt>{t("admin.sent30")}</dt><dd>{d.sent_30d}</dd></div>
        <div><dt>{t("admin.received30")}</dt><dd>{d.received_30d}</dd></div>
        <div><dt>{t("admin.lastMessage")}</dt><dd className="stat-text">{when(d.last_message_at)}</dd></div>
      </dl>

      <h3>{t("admin.numbers")}</h3>
      <div className="card table-wrap">
        <table>
          <tbody>
            {d.numbers.map((n) => (
              <tr key={n.id}>
                <td>{n.display_phone_number}<div className="muted small">{n.verified_name}</div></td>
                <td>{n.status}</td>
                <td>{t("admin.quality")}: {n.quality_rating}</td>
                <td>{t("admin.tier")}: {n.messaging_limit_tier ?? "—"}</td>
                <td className="small muted">WABA {n.waba_id}{n.coexistence ? ` · ${t("admin.coexistence")}` : ""}</td>
              </tr>
            ))}
            {d.numbers.length === 0 && <tr><td className="muted">{t("admin.noNumbers")}</td></tr>}
          </tbody>
        </table>
      </div>
      <p className="muted small">
        {t("admin.deliveries24")}:{" "}
        {deliveries.length ? deliveries.map(([k, v]) => `${k} ${v}`).join(", ") : t("admin.none")}
      </p>

      {d.subscription && <TenantPlan id={id} sub={d.subscription} />}

      <PaymentMode key={d.meta_payment_mode} id={id} mode={d.meta_payment_mode} />

      {d.status !== "closed" && (
        <form className="card inline-form" onSubmit={act}>
          <label className="field grow">
            {d.status === "active" ? t("admin.suspendReason") : t("admin.reactivateReason")}
            <input value={reason} onChange={(e) => setReason(e.target.value)} required minLength={5} maxLength={500} />
          </label>
          <button className={d.status === "active" ? "danger" : "primary"}>
            {d.status === "active" ? t("admin.suspend") : t("admin.reactivate")}
          </button>
        </form>
      )}
      {error && <div className="error">{error}</div>}

      <Conversations tenantId={id} />
    </>
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
      <div className="filters">
        <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label={t("admin.from")} />
        <input type="date" value={to} onChange={(e) => setTo(e.target.value)} aria-label={t("admin.to")} />
        <a href={`/internal/admin/invoices.csv${qs}`}>{t("admin.downloadCsv")}</a>
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
  const list = usePaged<MetaApiError>("meta-errors", "/internal/admin/meta-errors", new URLSearchParams({ limit: "50" }));
  const groups = Object.entries(
    list.rows.reduce<Record<string, { n: number; message: string; last: string }>>((acc, e) => {
      const k = String(e.code ?? e.http_status);
      const g = acc[k] ?? { n: 0, message: e.message ?? "", last: e.occurred_at };
      acc[k] = { n: g.n + 1, message: g.message, last: g.last > e.occurred_at ? g.last : e.occurred_at };
      return acc;
    }, {}),
  ).sort((a, b) => b[1].n - a[1].n);
  return (
    <>
      {groups.length > 0 && (
        <div className="card flush" style={{ marginBottom: 16 }}>
          <div className="chd"><div><h2>{t("admin.errorsByCode")}</h2><p>{t("admin.errorsByCodeHint", { count: list.rows.length })}</p></div></div>
          {groups.slice(0, 6).map(([code, g]) => (
            <div key={code} className="ql">
              <span className="chip muted"><code>{code}</code></span>
              <span className="t"><b>{g.message || "—"}</b><small>{t("admin.errorCount", { count: g.n })} · {when(g.last)}</small></span>
            </div>
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

function TenantPlan({ id, sub }: { id: string; sub: NonNullable<AdminTenantDetail["subscription"]> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [days, setDays] = useState("7");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
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
    <>
      <p className="small">
        {t("admin.subscription", {
          status: t(`admin.sub_${sub.status}`), plan: sub.plan_code ?? t("admin.noPlan"),
          date: new Date(sub.current_period_end).toLocaleDateString(),
        })}
        {!sub.usable && <> · <strong className="danger-text">{t("admin.sendingStopped")}</strong></>}
      </p>
      {sub.status === "trialing" && (
        <form className="card inline-form" onSubmit={extend}>
          <label className="field">
            {t("admin.extendDays")}
            <input type="number" min={1} max={90} value={days} onChange={(e) => setDays(e.target.value)} required />
          </label>
          <label className="field grow">
            {t("admin.extendReason")}
            <input value={reason} onChange={(e) => setReason(e.target.value)} required minLength={5} maxLength={500} />
          </label>
          <button>{t("admin.extendTrial")}</button>
        </form>
      )}
      {error && <div className="error">{error}</div>}
    </>
  );
}

const blankPlan = { code: "", name: "", price: "", seat: "", numbers: "1", seats: "3", ai: "0", razorpay: "", seatPlan: "", order: "0", active: true };

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
    });
  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!form) return;
    setError("");
    const price = rupeesToPaise(form.price), seat = rupeesToPaise(form.seat || "0");
    if (price === null || seat === null) {
      setError(t("admin.badPrice"));
      return;
    }
    try {
      await api("PUT", `/internal/admin/plans/${encodeURIComponent(form.code.trim())}`, {
        name: form.name, price_minor: price, extra_seat_minor: seat, included_numbers: Number(form.numbers),
        included_seats: Number(form.seats), ai_replies_per_month: Number(form.ai), razorpay_plan_id: form.razorpay.trim() || null,
        extra_seat_razorpay_plan_id: form.seatPlan.trim() || null, sort_order: Number(form.order),
        is_active: form.active,
      });
      setForm(null);
      await qc.invalidateQueries({ queryKey: ["admin", "plans"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const set = (k: keyof typeof blankPlan) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm({ ...form!, [k]: k === "active" ? e.target.checked : e.target.value });

  return (
    <>
      <p className="muted small">{t("admin.plansHelp")}</p>
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
                <td>{formatPaise(p.price_minor)}<div className="muted small">{t("admin.extraSeat", { price: formatPaise(p.extra_seat_minor) })}</div></td>
                <td className="small">{t("admin.includesValue", { numbers: p.included_numbers, seats: p.included_seats })} · {t("admin.aiRepliesValue", { count: p.ai_replies_per_month ?? 0 })}</td>
                <td className="small">{p.razorpay_plan_id ?? <span className="danger-text">{t("admin.notPayable")}</span>}</td>
                <td><button className="link" onClick={() => edit(p)}>{t("admin.edit")}</button></td>
              </tr>
            ))}
            {plans.data?.length === 0 && <tr><td colSpan={5} className="muted">{t("admin.noPlans")}</td></tr>}
          </tbody>
        </table>
      </div>
      {!form && <button className="primary" onClick={() => setForm(blankPlan)}>{t("admin.newPlan")}</button>}
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
            <label className="field">{t("admin.sortOrder")}<input type="number" value={form.order} onChange={set("order")} /></label>
            <label className="field check"><input type="checkbox" checked={form.active} onChange={set("active")} /> {t("admin.showToCustomers")}</label>
          </div>
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
