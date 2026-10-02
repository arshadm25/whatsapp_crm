import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import MetaFees from "../components/MetaFees";
import type { BillingOverview, BillingProfile, Invite, Invoice, Me, Role, TeamMember, Workspace } from "../api/types";
import { daysLeft, formatPaise, graceEnd } from "../lib/billing";
import { gstStates } from "../lib/gst";
import { assignable } from "../lib/team";

function message(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

export default function Settings() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const [tab, setTab] = useState<"team" | "workspace" | "billing" | "account">(
    new URLSearchParams(window.location.search).get("tab") === "billing" && role === "owner" ? "billing" : manager ? "team" : "account",
  );

  return (
    <section>
      <h1>{t("settings.title")}</h1>
      <div className="segmented tabs">
        {manager && <button className={tab === "team" ? "on" : ""} onClick={() => setTab("team")}>{t("settings.team")}</button>}
        {manager && <button className={tab === "workspace" ? "on" : ""} onClick={() => setTab("workspace")}>{t("settings.workspace")}</button>}
        {role === "owner" && <button className={tab === "billing" ? "on" : ""} onClick={() => setTab("billing")}>{t("settings.billing")}</button>}
        <button className={tab === "account" ? "on" : ""} onClick={() => setTab("account")}>{t("settings.account")}</button>
      </div>
      {tab === "team" && manager && <Team />}
      {tab === "workspace" && manager && <WorkspaceForm canEdit={role === "owner"} />}
      {tab === "billing" && role === "owner" && <Billing />}
      {tab === "account" && <Account />}
    </section>
  );
}

function Team() {
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
    });
  };

  return (
    <>
      <form className="card inline-form" onSubmit={invite}>
        <label className="field">
          {t("settings.inviteEmail")}
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required placeholder="name@business.in" />
        </label>
        <label className="field">
          {t("settings.role")}
          <select value={inviteRole} onChange={(e) => setInviteRole(e.target.value as Role)}>
            {roles.filter((r) => r !== "owner").map((r) => <option key={r} value={r}>{t(`settings.role_${r}`)}</option>)}
          </select>
        </label>
        <button className="primary">{t("settings.invite")}</button>
      </form>
      <p className="muted small">{t("settings.rolesHelp")}</p>
      {created?.link && (
        <div className="card notice">
          {t("settings.inviteSent", { email: created.email })}
          <div className="copy-row">
            <input readOnly value={created.link} onFocus={(e) => e.target.select()} />
            <button onClick={() => navigator.clipboard?.writeText(created.link!)}>{t("settings.copy")}</button>
          </div>
        </div>
      )}
      {error && <div className="error">{error}</div>}

      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t("settings.member")}</th>
              <th>{t("settings.role")}</th>
              <th>{t("settings.lastLogin")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {members.data?.map((m) => {
              const self = m.id === me.user.id;
              const editable = !self && roles.includes(m.role);
              return (
                <tr key={m.id}>
                  <td>
                    {m.name} {self && <span className="muted small">({t("settings.you")})</span>}
                    <div className="muted small">{m.email}</div>
                  </td>
                  <td>
                    {editable ? (
                      <select
                        value={m.role}
                        aria-label={t("settings.role")}
                        onChange={(e) => run(() => api("PATCH", `/internal/team/members/${m.id}`, { role: e.target.value }))}
                      >
                        {roles.map((r) => <option key={r} value={r}>{t(`settings.role_${r}`)}</option>)}
                      </select>
                    ) : (
                      t(`settings.role_${m.role}`)
                    )}
                  </td>
                  <td className="small">{m.last_login_at ? new Date(m.last_login_at).toLocaleString() : "—"}</td>
                  <td>
                    {editable && (
                      <button
                        className="link danger"
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
          </tbody>
        </table>
      </div>

      {invites.data && invites.data.length > 0 && (
        <>
          <h2>{t("settings.pendingInvites")}</h2>
          <div className="card table-wrap">
            <table>
              <tbody>
                {invites.data.map((i) => (
                  <tr key={i.id}>
                    <td>{i.email}</td>
                    <td>{t(`settings.role_${i.role}`)}</td>
                    <td className="small muted">{t("settings.invitedBy", { name: i.invited_by_name, date: new Date(i.expires_at).toLocaleDateString() })}</td>
                    <td>
                      {roles.includes(i.role) && (
                        <button className="link danger" onClick={() => run(() => api("DELETE", `/internal/team/invites/${i.id}`))}>
                          {t("settings.revoke")}
                        </button>
                      )}
                    </td>
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
  );
}

function Account() {
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
    <div className="grid-2">
      <form className="card form" onSubmit={saveName}>
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
      </form>
      <form className="card form" onSubmit={savePassword}>
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
      </form>
      <TwoFactor />
      {(note || error) && <div className={error ? "error" : "muted"}>{error || note}</div>}
    </div>
  );
}

// TwoFactor turns authenticator-app codes on or off for the signed-in user.
function TwoFactor() {
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
  const form: BillingProfile = draft ?? details.data?.profile ?? { legal_name: "", gstin: "", state_code: "", address: "" };
  const setField = (k: keyof BillingProfile) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
    setDraft({ ...form, [k]: e.target.value });
  const b = billing.data;
  if (!b) return <div className="card muted">{t("common.loading")}</div>;
  const sub = b.subscription;
  const end = new Date(sub.current_period_end).toLocaleDateString();

  const choose = async (code: string) => {
    setBusy(code);
    setError("");
    setNote("");
    try {
      const res = await api<{ payment_url?: string; scheduled?: boolean }>("POST", "/internal/billing/subscribe", { plan: code });
      if (res.payment_url) {
        window.location.href = res.payment_url;
        return;
      }
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
    setError("");
    setNote("");
    try {
      await api("PUT", "/internal/billing/profile", form);
      setDraft(null);
      setNote(t("billing.detailsSaved"));
      await qc.invalidateQueries({ queryKey: ["billing", "profile"] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const saveSeats = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setNote("");
    try {
      const res = await api<{ payment_url?: string; scheduled?: boolean }>("POST", "/internal/billing/seats", { extra: Number(extra) });
      if (res.payment_url) {
        window.location.href = res.payment_url;
        return;
      }
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
