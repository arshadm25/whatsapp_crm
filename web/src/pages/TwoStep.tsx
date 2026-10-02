import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type { Me } from "../api/types";
import { AuthCard, Field } from "./AuthForm";

// TwoStep asks for the authenticator code after a password login.
export default function TwoStep() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const me = useMe();
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  if (me.isLoading) return <div className="center muted">{t("common.loading")}</div>;
  if (!me.data) return <Navigate to="/login" replace />;
  if (!me.data.mfa_required) return <Navigate to="/" replace />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      qc.setQueryData(["me"], await api<Me>("POST", "/internal/auth/2fa/verify", { code }));
      navigate((location.state as { from?: string } | null)?.from ?? "/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.error"));
      setCode("");
    } finally {
      setBusy(false);
    }
  };
  const logout = async () => {
    await api("POST", "/internal/auth/logout");
    qc.clear();
    navigate("/login");
  };

  return (
    <AuthCard
      title={t("twoStep.title")}
      onSubmit={submit}
      footer={<button type="button" className="link" onClick={logout}>{t("twoStep.useOther")}</button>}
    >
      <p className="muted">{t("twoStep.lead")}</p>
      <Field
        label={t("twoStep.code")}
        inputMode="numeric"
        autoComplete="one-time-code"
        pattern="[0-9 ]{6,7}"
        maxLength={7}
        autoFocus
        required
        value={code}
        onChange={(e) => setCode(e.target.value)}
      />
      {error && <div className="error">{error}</div>}
      <button className="primary" disabled={busy}>{t("twoStep.verify")}</button>
    </AuthCard>
  );
}
