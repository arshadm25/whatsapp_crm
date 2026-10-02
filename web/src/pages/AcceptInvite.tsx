import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type { InviteInfo, Me } from "../api/types";
import { AuthCard, Field } from "./AuthForm";

export default function AcceptInvite() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const me = useMe();
  const info = useQuery({
    queryKey: ["invite", token],
    queryFn: () => api<InviteInfo>("GET", `/internal/auth/invites/${encodeURIComponent(token)}`),
    retry: false,
  });
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const accept = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await api<Me>("POST", "/internal/auth/invites/accept", { token, name, password });
      qc.clear();
      qc.setQueryData(["me"], res);
      navigate("/");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  if (info.isLoading || me.isLoading) return <div className="center muted">{t("common.loading")}</div>;
  if (info.error || !info.data) {
    return (
      <div className="auth-page">
        <div className="card auth-card">
          <div className="brand">{t("app.name")}</div>
          <p className="error">{info.error instanceof ApiError ? info.error.message : t("common.error")}</p>
          <Link className="button primary" to="/">{t("auth.continue")}</Link>
        </div>
      </div>
    );
  }

  const inv = info.data;
  const signedIn = me.data?.user.email.toLowerCase();
  const title = t("invite.title", { tenant: inv.tenant_name });
  const lead = <p className="muted">{t("invite.lead", { name: inv.invited_by_name, role: t(`settings.role_${inv.role}`) })}</p>;
  const here = `/invite?token=${encodeURIComponent(token)}`;

  if (signedIn && signedIn !== inv.email) {
    return (
      <AuthCard title={title} onSubmit={(e) => e.preventDefault()} footer={null}>
        {lead}
        <p className="error">{t("invite.otherAccount", { email: inv.email, current: signedIn })}</p>
      </AuthCard>
    );
  }
  if (!signedIn && inv.account_exists) {
    return (
      <AuthCard title={title} onSubmit={(e) => { e.preventDefault(); navigate("/login", { state: { from: here } }); }} footer={null}>
        {lead}
        <p>{t("invite.loginFirst", { email: inv.email })}</p>
        <button className="primary">{t("auth.login")}</button>
      </AuthCard>
    );
  }
  return (
    <AuthCard title={title} onSubmit={accept} footer={null}>
      {lead}
      {!signedIn && (
        <>
          <Field label={t("auth.email")} value={inv.email} disabled readOnly />
          <Field label={t("auth.name")} autoComplete="name" required value={name} onChange={(e) => setName(e.target.value)} />
          <Field label={t("auth.password")} type="password" autoComplete="new-password" required minLength={10}
            hint={t("auth.passwordHint")} value={password} onChange={(e) => setPassword(e.target.value)} />
        </>
      )}
      {error && <div className="error">{error}</div>}
      <button className="primary" disabled={busy}>{t("invite.accept")}</button>
    </AuthCard>
  );
}
