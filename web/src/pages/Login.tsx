import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import type { Me } from "../api/types";
import { AuthCard, Field } from "./AuthForm";

export default function Login() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const me = await api<Me>("POST", "/internal/auth/login", { email, password });
      qc.setQueryData(["me"], me);
      const from = (location.state as { from?: string } | null)?.from ?? "/";
      navigate(me.mfa_required ? "/2fa" : from, { state: { from } });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthCard
      title={t("auth.loginTitle")}
      onSubmit={submit}
      footer={<>{t("auth.noAccount")} <Link to="/signup">{t("auth.createOne")}</Link></>}
    >
      <Field label={t("auth.email")} type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
      <Field label={t("auth.password")} type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
      {error && <div className="error">{error}</div>}
      <button className="primary" disabled={busy}>{t("auth.login")}</button>
    </AuthCard>
  );
}
