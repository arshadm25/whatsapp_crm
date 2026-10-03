import { useState, type FormEvent } from "react";
import { canPopUp, openCheckout, type CheckoutReply } from "../lib/razorpayCheckout";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import MetaFees from "../components/MetaFees";
import type { BillingOverview, BillingProfile, Invite, Invoice, Me, NotificationSetting, Role, TeamMember, Workspace } from "../api/types";
import { daysLeft, formatPaise, graceEnd } from "../lib/billing";
import { gstStates } from "../lib/gst";
import { assignable } from "../lib/team";
import Icon, { type IconName } from "../components/Icon";
import { ago } from "../lib/time";

function initials(name: string) {
  const parts = name.split(/[\s@.]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "?") + (parts[1]?.[0] ?? "")).toUpperCase();
}

type Tab = "workspace" | "team" | "billing" | "security" | "notifications" | "account";

function message(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

export default function Settings() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const asked = new URLSearchParams(window.location.search).get("tab");
  const [tab, setTab] = useState<Tab>(
    asked === "billing" && role === "owner" ? "billing" : asked === "notifications" ? "notifications" : manager ? "team" : "account",
  );
  const tabs: Tab[] = [
    ...(manager ? (["workspace", "team"] as Tab[]) : []),
    ...(role === "owner" ? (["billing"] as Tab[]) : []),
    "security",
    "notifications",
    "account",
  ];

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("settings.title")}</h1>
          <p className="sub">{t("settings.intro")}</p>
        </div>
      </div>
      <div className="settings">
        <nav className="sn" aria-label={t("settings.title")}>
          {tabs.map((k) => (
            <button key={k} className={tab === k ? "on" : ""} aria-current={tab === k ? "page" : undefined} onClick={() => setTab(k)}>
              {t(`settings.tab_${k}`)}
            </button>
          ))}
        </nav>
        <div className="stack">
          {tab === "team" && manager && <Team onBilling={() => setTab("billing")} />}
          {tab === "workspace" && manager && <WorkspaceForm canEdit={role === "owner"} />}
          {tab === "billing" && role === "owner" && <Billing />}
          {tab === "security" && <Account part="security" />}
          {tab === "notifications" && <Notifications />}
          {tab === "account" && <Account part="profile" />}
        </div>
      </div>
    </section>
  );
}

const ROLE_ICONS: Record<Role, { icon: IconName; tone: string }> = {
  owner: { icon: "shield", tone: "" },
  admin: { icon: "settings", tone: "bl" },
  agent: { icon: "inbox", tone: "am" },
  developer: { icon: "code", tone: "gy" },
};

function Team({ onBilling }: { onBilling: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const me = useMe().data!;
  const actor = me.tenant?.role;
  const members = useQuery({
    queryKey: ["team"],
    queryFn: async () => (await api<{ data: TeamMember[] }>("GET", "/internal/team/members")).data,
  });
  const invites = useQuery({
    queryKey: ["invites"],
    queryFn: async () => (await api<{ data: Invite[] }>("GET", "/internal/team/invites")).data,
  });
  const [inviting, setInviting] = useState(false);
  const [email, setEmail] = useState("");
  const [inviteRole, setInviteRole] = useState<Role>("agent");
  const [created, setCreated] = useState<Invite | null>(null);
  const [error, setError] = useState("");
  const roles = assignable(actor);

  const run = async (fn: () => Promise<unknown>) => {
    setError("");
    try {
      await fn();
      await qc.invalidateQueries({ queryKey: ["team"] });
      await qc.invalidateQueries({ queryKey: ["invites"] });
    } catch (e) {
      setError(message(e, t("common.error")));
    }
  };
  const invite = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      setCreated(await api<Invite>("POST", "/internal/team/invites", { email, role: inviteRole }));
      setEmail("");
      setInviting(false);
    });
  };

  return (
    <>
      <div className="card flush">
        <div className="chd">
          <div>
            <h2>{t("settings.teamMembers")}</h2>
            <p>{t("settings.memberCount", { count: members.data?.length ?? 0 })} · {t("settings.inviteCount", { count: invites.data?.length ?? 0 })}</p>
          </div>
          <button className="primary sm" onClick={() => setInviting(!inviting)}><Icon name="userPlus" size="s" />{t("settings.inviteMember")}</button>
        </div>
        {inviting && (
          <form className="inline-form in-card" onSubmit={invite}>
            <label className="field">
              {t("settings.inviteEmail")}
              <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required placeholder="name@business.in" autoFocus />
            </label>
            <label className="field">
              {t("settings.role")}
              <select value={inviteRole} onChange={(e) => setInviteRole(e.target.value as Role)}>
                {roles.filter((r) => r !== "owner").map((r) => <option key={r} value={r}>{t(`settings.role_${r}`)}</option>)}
              </select>
            </label>
            <div className="actions">
              <button type="button" onClick={() => setInviting(false)}>{t("common.cancel")}</button>
              <button className="primary">{t("settings.invite")}</button>
            </div>
          </form>
        )}
        {created?.link && (
          <div className="notice card">
            {t("settings.inviteSent", { email: created.email })}
            <div className="copy-row">
              <input readOnly value={created.link} onFocus={(e) => e.target.select()} />
              <button onClick={() => navigator.clipboard?.writeText(created.link!)}>{t("settings.copy")}</button>
            </div>
          </div>
        )}
        {error && <div className="error">{error}</div>}
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("settings.member")}</th>
                <th>{t("settings.role")}</th>
                <th>{t("settings.status")}</th>
                <th>{t("settings.lastActive")}</th>
                <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {members.data?.map((m) => {
                const self = m.id === me.user.id;
                const editable = !self && roles.includes(m.role);
                return (
                  <tr key={m.id}>
                    <td>
                      <div className="who">
                        <span className="av">{initials(m.name)}</span>
                        <span><b>{m.name} {self && <span className="chip gy">{t("settings.you")}</span>}</b><small>{m.email}</small></span>
                      </div>
                    </td>
                    <td>
                      {editable ? (
                        <select
                          className="role"
                          value={m.role}
                          aria-label={t("settings.role")}
                          onChange={(e) => run(() => api("PATCH", `/internal/team/members/${m.id}`, { role: e.target.value }))}
                        >
                          {roles.map((r) => <option key={r} value={r}>{t(`settings.role_${r}`)}</option>)}
                        </select>
                      ) : (
                        <span className="role">{t(`settings.role_${m.role}`)}</span>
                      )}
                    </td>
                    <td><span className="pill ok">{t("settings.active")}</span></td>
                    <td>{self ? t("settings.now") : m.last_login_at ? ago(m.last_login_at, t) : "—"}</td>
                    <td className="r">
                      {editable && (
                        <button
                          className="sm bdg"
                          onClick={() => window.confirm(t("settings.confirmRemove", { name: m.name })) &&
                            run(() => api("DELETE", `/internal/team/members/${m.id}`))}
                        >
                          {t("settings.remove")}
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
              {invites.data?.map((i) => (
                <tr key={i.id}>
                  <td>
                    <div className="who">
                      <span className="av">{initials(i.email)}</span>
                      <span><b>{i.email}</b><small>{t("settings.invitedBy", { name: i.invited_by_name, date: new Date(i.expires_at).toLocaleDateString() })}</small></span>
                    </div>
                  </td>
                  <td><span className="role">{t(`settings.role_${i.role}`)}</span></td>
                  <td><span className="pill wa">{t("settings.invited")}</span></td>
                  <td className="muted">{t("settings.sentAgo", { ago: ago(i.created_at, t) })}</td>
                  <td className="r">
                    {roles.includes(i.role) && (
                      <span className="actions" style={{ justifyContent: "flex-end" }}>
                        <button className="sm" onClick={() => run(async () => setCreated(await api<Invite>("POST", `/internal/team/invites/${i.id}/resend`)))}>
                          {t("settings.resend")}
                        </button>
                        <button className="sm" onClick={() => run(() => api("DELETE", `/internal/team/invites/${i.id}`))}>
                          {t("settings.revoke")}
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="grid g2">
        <div className="card flush">
          <div className="chd"><div><h2>{t("settings.rolesTitle")}</h2><p>{t("settings.rolesSub")}</p></div></div>
          <div className="cb roles">
            {(["owner", "admin", "agent", "developer"] as const).map((r) => (
              <div key={r} className="rl">
                <span className={`ic ${ROLE_ICONS[r].tone}`}><Icon name={ROLE_ICONS[r].icon} size="s" /></span>
                <span><b>{t(`settings.role_${r}`)}</b><span className="d">{t(`settings.roleDesc_${r}`)}</span></span>
              </div>
            ))}
          </div>
        </div>
        {actor === "owner" && <BillingSummary onManage={onBilling} />}
      </div>
    </>
  );
}

// BillingSummary is the owner's plan at a glance, next to the roles on the Team tab.
function BillingSummary({ onManage }: { onManage: () => void }) {
  const { t } = useTranslation();
  const billing = useQuery({ queryKey: ["billing"], queryFn: () => api<BillingOverview>("GET", "/internal/billing") });
  const b = billing.data;
  return (
    <div className="card flush">
      <div className="chd">
        <div><h2>{t("settings.billing")}</h2><p>{t("settings.billingSub")}</p></div>
        <button className="link" onClick={onManage}>{t("settings.manage")}</button>
      </div>
      <div className="cb">
        {b ? (
          <dl className="kv">
            <dt>{t("billing.planLabel")}</dt><dd>{b.plan?.name ?? t("billing.noPlanShort")}</dd>
            <dt>{t("billing.seatsLabel")}</dt><dd>{b.plan ? t("billing.seatsOf", { used: b.seats, total: b.plan.included_seats + b.subscription.extra_seats }) : b.seats}</dd>
            <dt>{b.subscription.status === "trialing" ? t("billing.trialEnds") : t("billing.nextInvoice")}</dt>
            <dd>{new Date(b.subscription.current_period_end).toLocaleDateString()}</dd>
          </dl>
        ) : (
          <div className="muted">{t("common.loading")}</div>
        )}
      </div>
      <div className="cf">{t("settings.metaFeesNote")}</div>
    </div>
  );
}

function WorkspaceForm({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const ws = useQuery({ queryKey: ["workspace"], queryFn: () => api<Workspace>("GET", "/internal/team/workspace") });
  const [draft, setDraft] = useState<Workspace | null>(null);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  if (!ws.data) return <div className="card muted">{t("common.loading")}</div>;
  const form = draft ?? ws.data;
  const zones = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [form.time_zone];

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setNote("");
    try {
      qc.setQueryData(["workspace"], await api<Workspace>("PATCH", "/internal/team/workspace", form));
      await qc.invalidateQueries({ queryKey: ["me"] });
      setDraft(null);
      setNote(t("settings.saved"));
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };

  return (
    <>
    <form className="card form" onSubmit={save}>
      <fieldset disabled={!canEdit} className="plain">
        <label className="field">
          {t("settings.businessName")}
          <input value={form.name} onChange={(e) => setDraft({ ...form, name: e.target.value })} required maxLength={160} />
        </label>
        <label className="field">
          {t("settings.legalName")}
          <input value={form.legal_name ?? ""} onChange={(e) => setDraft({ ...form, legal_name: e.target.value })} maxLength={200} />
        </label>
        <label className="field">
          {t("settings.timeZone")}
          <select value={form.time_zone} onChange={(e) => setDraft({ ...form, time_zone: e.target.value })}>
            {(zones.includes(form.time_zone) ? zones : [form.time_zone, ...zones]).map((z) => <option key={z}>{z}</option>)}
          </select>
          <span className="muted small">{t("settings.timeZoneHint")}</span>
        </label>
        <label className="field">
          {t("settings.retention")}
          <select
            value={form.message_retention_days ?? ""}
            onChange={(e) => setDraft({ ...form, message_retention_days: e.target.value ? Number(e.target.value) : null })}
          >
            <option value="">{t("settings.retentionForever")}</option>
            {[30, 90, 180, 365, 730].map((d) => <option key={d} value={d}>{t("settings.retentionDays", { count: d })}</option>)}
            {form.message_retention_days != null && ![30, 90, 180, 365, 730].includes(form.message_retention_days) && (
              <option value={form.message_retention_days}>{t("settings.retentionDays", { count: form.message_retention_days })}</option>
            )}
          </select>
          <span className="muted small">{t("settings.retentionHint")}</span>
        </label>
      </fieldset>
      {!canEdit && <p className="muted small">{t("settings.ownerOnly")}</p>}
      {error && <div className="error">{error}</div>}
      {canEdit && (
        <div className="actions">
          {note && <span className="muted small">{note}</span>}
          <button className="primary" disabled={!draft}>{t("settings.save")}</button>
        </div>
      )}
    </form>
    <RequireTwoFactor workspace={ws.data} canEdit={canEdit} />
    </>
  );
}

// RequireTwoFactor is the owner's switch that makes every member use two-step verification.
function RequireTwoFactor({ workspace, canEdit }: { workspace: Workspace; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const me = useMe().data!;
  const [error, setError] = useState("");
  const toggle = async (required: boolean) => {
    setError("");
    try {
      qc.setQueryData(["workspace"], await api<Workspace>("PUT", "/internal/team/workspace/two-factor", { required }));
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  return (
    <div className="card form">
      <h2>{t("settings.require2fa")}</h2>
      <p className="muted small">{t("settings.require2faHint")}</p>
      <label className="check">
        <input
          type="checkbox"
          checked={workspace.require_two_factor}
          disabled={!canEdit || (!workspace.require_two_factor && !me.user.two_factor_enabled)}
          onChange={(e) => toggle(e.target.checked)}
        />
        {t("settings.require2faLabel")}
      </label>
      {canEdit && !me.user.two_factor_enabled && <p className="muted small">{t("settings.require2faFirst")}</p>}
      {error && <div className="error">{error}</div>}
    </div>
  );
}

// Notifications lets each member choose which notifications they get in the app and by email.
function Notifications() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const settings = useQuery({
    queryKey: ["notification-settings"],
    queryFn: async () => (await api<{ data: NotificationSetting[] }>("GET", "/internal/notifications/settings")).data,
  });
  const [error, setError] = useState("");
  if (!settings.data) return <div className="card muted">{t("common.loading")}</div>;

  const change = async (next: NotificationSetting) => {
    setError("");
    try {
      const saved = await api<{ data: NotificationSetting[] }>("PUT", "/internal/notifications/settings", { data: [next] });
      qc.setQueryData(["notification-settings"], saved.data);
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };

  return (
    <div className="card flush">
      <div className="chd"><div><h2>{t("settings.notifications")}</h2></div></div>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("settings.notifyWhen")}</th>
              <th>{t("settings.notifyInApp")}</th>
              <th>{t("settings.notifyEmail")}</th>
            </tr>
          </thead>
          <tbody>
            {settings.data.map((s) => (
              <tr key={s.kind}>
                <td>
                  <b>{t(`settings.notify_${s.kind}`)}</b>
                  <div className="muted small">{t(`settings.notifyHint_${s.kind}`)}</div>
                </td>
                <td>
                  <input type="checkbox" aria-label={t("settings.notifyInApp")} checked={s.in_app} onChange={(e) => change({ ...s, in_app: e.target.checked })} />
                </td>
                <td>
                  <input type="checkbox" aria-label={t("settings.notifyEmail")} checked={s.email} onChange={(e) => change({ ...s, email: e.target.checked })} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {error && <div className="error">{error}</div>}
    </div>
  );
}

function Account({ part }: { part: "profile" | "security" }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const me = useMe().data!;
  const [name, setName] = useState(me.user.name);
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState("");

  const saveName = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setNote("");
    try {
      qc.setQueryData(["me"], await api<Me>("PATCH", "/internal/auth/profile", { name }));
      setNote(t("settings.saved"));
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const savePassword = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setNote("");
    try {
      await api("POST", "/internal/auth/password", { current_password: current, new_password: next });
      setCurrent("");
      setNext("");
      setNote(t("settings.passwordChanged"));
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };

  return (
    <div className="grid g2">
      {part === "profile" && <form className="card form" onSubmit={saveName}>
        <h2>{t("settings.profile")}</h2>
        <label className="field">
          {t("auth.name")}
          <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={120} />
        </label>
        <label className="field">
          {t("auth.email")}
          <input value={me.user.email} disabled />
        </label>
        <div className="actions"><button className="primary" disabled={name.trim() === me.user.name}>{t("settings.save")}</button></div>
      </form>}
      {part === "security" && <form className="card form" onSubmit={savePassword}>
        <h2>{t("settings.password")}</h2>
        <label className="field">
          {t("settings.currentPassword")}
          <input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
        </label>
        <label className="field">
          {t("settings.newPassword")}
          <input type="password" autoComplete="new-password" minLength={10} value={next} onChange={(e) => setNext(e.target.value)} required />
          <span className="muted small">{t("settings.passwordHint")}</span>
        </label>
        <div className="actions"><button className="primary">{t("settings.changePassword")}</button></div>
      </form>}
      {part === "security" && <TwoFactor />}
      {(note || error) && <div className={error ? "error" : "muted"}>{error || note}</div>}
    </div>
  );
}

// TwoFactor turns authenticator-app codes on or off for the signed-in user.
export function TwoFactor() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const me = useMe().data!;
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const on = me.user.two_factor_enabled;

  const start = async () => {
    setError("");
    setNote("");
    try {
      setSetup(await api<{ secret: string; uri: string }>("POST", "/internal/auth/2fa/setup"));
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      const next = on
        ? await api<Me>("POST", "/internal/auth/2fa/disable", { code })
        : await api<Me>("POST", "/internal/auth/2fa/enable", { secret: setup?.secret, code });
      qc.setQueryData(["me"], next);
      setSetup(null);
      setCode("");
      setNote(on ? t("settings.twoStepDisabled") : t("settings.twoStepEnabled"));
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const codeField = (label: string) => (
    <label className="field">
      {label}
      <input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]{6,7}" maxLength={7} value={code} onChange={(e) => setCode(e.target.value)} required />
    </label>
  );

  return (
    <form className="card form" onSubmit={submit}>
      <h2>{t("settings.twoStep")}</h2>
      <p className="muted small">{on ? t("settings.twoStepOn") : t("settings.twoStepOff")}</p>
      {on && !me.user.is_platform_admin && (
        <>
          {codeField(t("settings.twoStepDisableHint"))}
          <div className="actions"><button className="danger">{t("settings.twoStepDisable")}</button></div>
        </>
      )}
      {on && me.user.is_platform_admin && <p className="muted small">{t("settings.twoStepAdmin")}</p>}
      {!on && !setup && (
        <div className="actions"><button type="button" className="primary" onClick={start}>{t("settings.twoStepStart")}</button></div>
      )}
      {!on && setup && (
        <>
          <p className="small">{t("settings.twoStepScan")}</p>
          <div className="field">
            {t("settings.twoStepKey")}
            <div className="secret">{setup.secret.replace(/(.{4})/g, "$1 ").trim()}</div>
            <a href={setup.uri} className="small">{t("settings.twoStepLink")}</a>
          </div>
          {codeField(t("settings.twoStepCode"))}
          <div className="actions"><button className="primary">{t("settings.twoStepEnable")}</button></div>
        </>
      )}
      {(note || error) && <div className={error ? "error" : "muted small"}>{error || note}</div>}
    </form>
  );
}

// Billing is the owner's view of the Ecogo plan: trial, current plan, usage against it, and
// the plans to choose from. Payment happens on Razorpay's page.
function Billing() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const billing = useQuery({ queryKey: ["billing"], queryFn: () => api<BillingOverview>("GET", "/internal/billing") });
  const [busy, setBusy] = useState("");
  const [extra, setExtra] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const details = useQuery({
    queryKey: ["billing", "profile"],
    queryFn: () => api<{ profile: BillingProfile | null; invoicing: boolean }>("GET", "/internal/billing/profile"),
  });
  const invoices = useQuery({
    queryKey: ["billing", "invoices"],
    queryFn: async () => (await api<{ data: Invoice[] }>("GET", "/internal/billing/invoices")).data,
  });
  const [draft, setDraft] = useState<BillingProfile | null>(null);
  // Shown beside the Save button: the page-level message is out of sight at the bottom of a long form.
  const [detailsMsg, setDetailsMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const form: BillingProfile = draft ?? details.data?.profile ?? { legal_name: "", gstin: "", state_code: "", address: "" };
  const setField = (k: keyof BillingProfile) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
    setDraft({ ...form, [k]: e.target.value });
  const b = billing.data;
  if (!b) return <div className="card muted">{t("common.loading")}</div>;
  const sub = b.subscription;
  const end = new Date(sub.current_period_end).toLocaleDateString();

  // pay opens Razorpay's pop-up (or the hosted page for older replies); false means no payment was needed.
  const pay = async (res: CheckoutReply): Promise<boolean> => {
    if (canPopUp(res)) {
      await openCheckout(res, "Ecogo WhatsApp", () => {
        setNote(t("billing.paymentReceived"));
        void qc.invalidateQueries({ queryKey: ["billing"] });
      });
      return true;
    }
    if (res.payment_url) {
      window.location.href = res.payment_url;
      return true;
    }
    return false;
  };
  const choose = async (code: string) => {
    setBusy(code);
    setError("");
    setNote("");
    try {
      const res = await api<CheckoutReply & { scheduled?: boolean }>("POST", "/internal/billing/subscribe", { plan: code });
      if (await pay(res)) return;
      setNote(t("billing.changeScheduled"));
      await qc.invalidateQueries({ queryKey: ["billing"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    } finally {
      setBusy("");
    }
  };
  const saveDetails = async (e: FormEvent) => {
    e.preventDefault();
    setDetailsMsg(null);
    try {
      await api("PUT", "/internal/billing/profile", form);
      setDraft(null);
      setDetailsMsg({ ok: true, text: t("billing.detailsSaved") });
      await qc.invalidateQueries({ queryKey: ["billing", "profile"] });
    } catch (err) {
      setDetailsMsg({ ok: false, text: message(err, t("common.error")) });
    }
  };
  const saveSeats = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setNote("");
    try {
      const res = await api<CheckoutReply & { scheduled?: boolean }>("POST", "/internal/billing/seats", { extra: Number(extra) });
      if (await pay(res)) return;
      setNote(res.scheduled ? t("billing.extraSeatsScheduled") : t("billing.extraSeatsDone"));
      setExtra(null);
      await qc.invalidateQueries({ queryKey: ["billing"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const cancel = async () => {
    if (!window.confirm(sub.status === "trialing" ? t("billing.confirmDropPlan") : t("billing.confirmCancel", { date: end }))) return;
    setError("");
    try {
      await api("POST", "/internal/billing/cancel");
      await qc.invalidateQueries({ queryKey: ["billing"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };

  let status: string;
  if (!sub.usable) status = t("billing.ended");
  else if (sub.status === "trialing") status = t("billing.trial", { count: daysLeft(sub.current_period_end), date: end });
  else if (sub.status === "past_due") status = t("billing.pastDue", { date: graceEnd(sub).toLocaleDateString() });
  else if (sub.status === "cancelled" || sub.cancel_at_period_end) status = t("billing.endsOn", { date: end });
  else status = t("billing.renews", { date: end });

  return (
    <>
      <div className={`card ${sub.usable && sub.status !== "past_due" ? "" : "notice warn"}`}>
        <h2>{b.plan ? t("billing.onPlan", { plan: b.plan.name }) : t("billing.noPlan")}</h2>
        <p>{status}</p>
        {sub.status === "trialing" && b.plan && <p className="muted small">{t("billing.chosenDuringTrial", { plan: b.plan.name, date: end })}</p>}
        {sub.payment_pending && <p className="muted small">{t("billing.paymentPending")}</p>}
        <p className="muted small">
          {t("billing.usage", {
            numbers: b.connected_numbers, includedNumbers: b.plan?.included_numbers ?? "—",
            seats: b.seats, includedSeats: b.plan ? b.plan.included_seats + sub.extra_seats : "—",
          })}
        </p>
        {b.plan && sub.status !== "cancelled" && !sub.cancel_at_period_end && (
          <button className="link danger" onClick={cancel}>{sub.status === "trialing" ? t("billing.dropPlan") : t("billing.cancel")}</button>
        )}
      </div>
      {b.plan?.extra_seats_available && sub.status === "active" && !sub.cancel_at_period_end && (
        <form className="card form" onSubmit={saveSeats}>
          <h3>{t("billing.extraSeatsTitle")}</h3>
          <p className="muted small">
            {t("billing.extraSeatsLead", { included: b.plan.included_seats, price: formatPaise(b.plan.extra_seat_minor), members: b.seats })}
          </p>
          <label className="field">
            {t("billing.extraSeatsCount")}
            <input type="number" min={0} max={500} value={extra ?? String(sub.extra_seats)} onChange={(e) => setExtra(e.target.value)} />
          </label>
          <div className="actions">
            <button className="primary" disabled={extra === null || extra === String(sub.extra_seats)}>{t("billing.extraSeatsSave")}</button>
          </div>
        </form>
      )}
      {!b.payments_enabled && <div className="card notice">{t("billing.paymentsOff")}</div>}
      {error && <div className="error">{error}</div>}
      {note && <div className="muted">{note}</div>}
      <h2>{t("billing.plans")}</h2>
      {sub.status === "trialing" && b.plans.length > 0 && <p className="muted small">{t("billing.verifyNote", { date: end })}</p>}
      {b.plans.length === 0 ? (
        <div className="card muted">{t("billing.noPlans")}</div>
      ) : (
        <div className="plans">
          {b.plans.map((p) => {
            const current = b.plan?.code === p.code && sub.status !== "cancelled";
            return (
              <div key={p.code} className={`card plan ${current ? "current" : ""}`}>
                <h3>{p.name}</h3>
                <div className="price">{formatPaise(p.price_minor)}<span className="muted small"> {t("billing.perMonth")}</span></div>
                <ul className="small">
                  <li>{t("billing.numbers", { count: p.included_numbers })}</li>
                  <li>{t("billing.seats", { count: p.included_seats })}</li>
                  {p.extra_seat_minor > 0 && <li>{t("billing.extraSeat", { price: formatPaise(p.extra_seat_minor) })}</li>}
                </ul>
                {current ? (
                  <span className="pill">{t("billing.current")}</span>
                ) : (
                  <button className="primary" disabled={!b.payments_enabled || busy !== ""} onClick={() => choose(p.code)}>
                    {busy === p.code ? t("billing.opening") : b.plan && sub.status === "active" ? t("billing.switch") : t("billing.choose")}
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}
      <form className="card form" onSubmit={saveDetails}>
        <h3>{t("billing.details")}</h3>
        <p className="muted small">{t("billing.detailsLead")}</p>
        <label className="field">{t("billing.legalName")}<input value={form.legal_name} onChange={setField("legal_name")} required maxLength={200} /></label>
        <label className="field">{t("billing.gstin")}<input value={form.gstin} onChange={setField("gstin")} maxLength={15} placeholder="27AAPFU0939F1ZV" /></label>
        <label className="field">
          {t("billing.state")}
          <select value={form.gstin ? form.gstin.slice(0, 2) : form.state_code} onChange={setField("state_code")} required disabled={form.gstin.length === 15}>
            <option value="">{t("billing.chooseState")}</option>
            {gstStates.map(([code, name]) => <option key={code} value={code}>{name}</option>)}
          </select>
        </label>
        <label className="field">{t("billing.address")}<textarea value={form.address} onChange={setField("address")} required maxLength={500} rows={3} /></label>
        {detailsMsg && <div className={detailsMsg.ok ? "muted" : "field-error"}>{detailsMsg.text}</div>}
        <div className="actions"><button className="primary">{t("billing.saveDetails")}</button></div>
      </form>
      <h2>{t("billing.invoices")}</h2>
      {details.data && !details.data.invoicing && <div className="card muted small">{t("billing.invoicingOff")}</div>}
      {invoices.data?.length === 0 ? (
        <div className="card muted">{t("billing.noInvoices")}</div>
      ) : (
        <div className="card">
          <table>
            <tbody>
              {invoices.data?.map((i) => (
                <tr key={i.id}>
                  <td>{i.number}<div className="muted small">{i.description}</div></td>
                  <td>{new Date(i.issued_at).toLocaleDateString()}</td>
                  <td>{formatPaise(i.total_minor)}</td>
                  <td><a href={`/internal/billing/invoices/${i.id}/view`} target="_blank" rel="noreferrer">{t("billing.viewInvoice")}</a></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <MetaFees />
    </>
  );
}
