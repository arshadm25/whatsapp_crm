import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import type { Me } from "../api/types";
import { AuthCard, Field } from "./AuthForm";

export default function Signup() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [form, setForm] = useState({ name: "", email: "", password: "", business_name: "" });
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });
  const fieldError = (param: string) => (error?.param === param ? error.message : undefined);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const me = await api<Me>("POST", "/internal/auth/signup", form);
      qc.setQueryData(["me"], me);
      navigate("/");
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, "error", t("common.error")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthCard
      title={t("auth.signupTitle")}
      onSubmit={submit}
      footer={<>{t("auth.haveAccount")} <Link to="/login">{t("auth.logIn")}</Link></>}
    >
      <Field label={t("auth.name")} autoComplete="name" required value={form.name} onChange={set("name")} error={fieldError("name")} />
      <Field label={t("auth.businessName")} autoComplete="organization" required value={form.business_name} onChange={set("business_name")} error={fieldError("business_name")} />
      <Field label={t("auth.email")} type="email" autoComplete="email" required value={form.email} onChange={set("email")} error={fieldError("email")} />
      <Field label={t("auth.password")} type="password" autoComplete="new-password" required minLength={10} hint={t("auth.passwordHint")} value={form.password} onChange={set("password")} error={fieldError("password")} />
      {error && !error.param && <div className="error">{error.message}</div>}
      <button className="primary" disabled={busy}>{t("auth.signup")}</button>
    </AuthCard>
  );
}
