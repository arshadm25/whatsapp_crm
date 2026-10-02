import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type { Invite, Me, Role, TeamMember, Workspace } from "../api/types";
import { assignable } from "../lib/team";

function message(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

export default function Settings() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const [tab, setTab] = useState<"team" | "workspace" | "account">(manager ? "team" : "account");

  return (
    <section>
      <h1>{t("settings.title")}</h1>
      <div className="segmented tabs">
        {manager && <button className={tab === "team" ? "on" : ""} onClick={() => setTab("team")}>{t("settings.team")}</button>}
        {manager && <button className={tab === "workspace" ? "on" : ""} onClick={() => setTab("workspace")}>{t("settings.workspace")}</button>}
        <button className={tab === "account" ? "on" : ""} onClick={() => setTab("account")}>{t("settings.account")}</button>
      </div>
      {tab === "team" && manager && <Team />}
      {tab === "workspace" && manager && <WorkspaceForm canEdit={role === "owner"} />}
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
      {(note || error) && <div className={error ? "error" : "muted"}>{error || note}</div>}
    </div>
  );
}
