import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api, ApiError } from "../api/client";
import { AuthCard, Field } from "./AuthForm";

// ForgotPassword asks for the email to send a reset link to.
export function ForgotPassword() {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("POST", "/internal/auth/password/forgot", { email });
      setSent(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthCard title={t("reset.forgotTitle")} onSubmit={submit} footer={<Link to="/login">{t("reset.backToLogin")}</Link>}>
      {sent ? (
        <p>{t("reset.sent", { email })}</p>
      ) : (
        <>
          <p className="muted">{t("reset.forgotLead")}</p>
          <Field label={t("auth.email")} type="email" autoComplete="email" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
          {error && <div className="error">{error}</div>}
          <button className="primary" disabled={busy}>{t("reset.send")}</button>
        </>
      )}
    </AuthCard>
  );
}

// ResetPassword sets a new password from the emailed link.
export function ResetPassword() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [password, setPassword] = useState("");
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("POST", "/internal/auth/password/reset", { token, password });
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthCard title={t("reset.title")} onSubmit={submit} footer={<Link to="/login">{t("reset.backToLogin")}</Link>}>
      {done ? (
        <p>{t("reset.done")} <Link to="/login">{t("auth.login")}</Link></p>
      ) : (
        <>
          <Field
            label={t("settings.newPassword")}
            type="password"
            autoComplete="new-password"
            minLength={10}
            required
            autoFocus
            hint={t("settings.passwordHint")}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {error && <div className="error">{error}</div>}
          <button className="primary" disabled={busy || !token}>{t("reset.save")}</button>
          {!token && <Link to="/forgot-password" className="small">{t("reset.forgotTitle")}</Link>}
        </>
      )}
    </AuthCard>
  );
}
