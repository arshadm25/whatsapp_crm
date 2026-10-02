import { useState, type FormEvent } from "react";
import { Link, Navigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type {
  AdminConversation, AdminTenant, AdminTenantDetail, AuditEntry, Message, MetaApiError, Page, WebhookHealth,
} from "../api/types";
import { messageText } from "../lib/messages";

function message(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

const when = (s: string | null) => (s ? new Date(s).toLocaleString() : "—");

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

type Tab = "tenants" | "webhooks" | "metaErrors" | "audit";

export default function Admin() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const [tab, setTab] = useState<Tab>("tenants");
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
      <h1>{t("admin.title")}</h1>
      <div className="segmented tabs">
        {(["tenants", "webhooks", "metaErrors", "audit"] as Tab[]).map((k) => (
          <button key={k} className={tab === k ? "on" : ""} onClick={() => setTab(k)}>{t(`admin.tab_${k}`)}</button>
        ))}
      </div>
      {tab === "tenants" && <Tenants />}
      {tab === "webhooks" && <Webhooks />}
      {tab === "metaErrors" && <MetaErrors />}
      {tab === "audit" && <Audit />}
    </section>
  );
}

function Tenants() {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [open, setOpen] = useState<string | null>(null);
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
                <td>
                  {w.name}
                  <div className="muted small">{w.slug}</div>
                </td>
                <td><span className={`pill ${w.status}`}>{t(`admin.status_${w.status}`)}</span></td>
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
          {["6", "24", "72", "168"].map((v) => <option key={v} value={v}>{t("admin.lastHours", { count: Number(v) })}</option>)}
        </select>
      </div>
      {total && (
        <dl className="stats">
          <div><dt>{t("admin.received")}</dt><dd>{total.received}</dd></div>
          <div><dt>{t("admin.failed")}</dt><dd>{total.failed}</dd></div>
          <div><dt>{t("admin.pending")}</dt><dd>{total.pending}</dd></div>
        </dl>
      )}
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

function MetaErrors() {
  const { t } = useTranslation();
  const list = usePaged<MetaApiError>("meta-errors", "/internal/admin/meta-errors", new URLSearchParams({ limit: "50" }));
  return (
    <>
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
