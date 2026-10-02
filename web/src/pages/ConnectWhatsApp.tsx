import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useConfig } from "../api/hooks";
import type { OnboardingFlow, OnboardingSession } from "../api/types";
import { launchEmbeddedSignup, loadFacebookSdk } from "../lib/embeddedSignup";

const STEPS: Record<OnboardingFlow, string[]> = {
  standard: ["code_received", "token_exchanged", "webhooks_subscribed", "number_registered", "details_synced", "completed"],
  coexistence: [
    "code_received", "token_exchanged", "webhooks_subscribed", "contacts_sync_requested",
    "history_sync_requested", "details_synced", "completed",
  ],
};

export default function ConnectWhatsApp() {
  const { t } = useTranslation();
  const config = useConfig();
  const [params, setParams] = useSearchParams();
  const sessionId = params.get("session");
  const [flow, setFlow] = useState<OnboardingFlow>("standard");
  const [sdkReady, setSdkReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const meta = config.data?.meta;
  const configured = !!(meta?.app_id && meta?.config_id);

  // Load Meta's SDK up front: FB.login must run inside the click, or browsers block the popup.
  useEffect(() => {
    if (!configured || !meta) return;
    loadFacebookSdk(meta.app_id, meta.graph_api_version)
      .then(() => setSdkReady(true))
      .catch((e: Error) => setError(e.message));
  }, [configured, meta]);

  const start = async () => {
    setBusy(true);
    setError("");
    const created = api<OnboardingSession>("POST", "/internal/onboarding/sessions", { flow });
    const popup = launchEmbeddedSignup(meta!.config_id, flow);
    try {
      const [session, result] = await Promise.all([created, popup]);
      const ev = result.event;
      if (ev?.kind === "finish" && result.code) {
        await api("POST", `/internal/onboarding/sessions/${session.id}/complete`, {
          code: result.code,
          waba_id: ev.wabaId,
          phone_number_id: ev.phoneNumberId ?? "",
          business_id: ev.businessId ?? "",
        }).catch((e) => {
          if (!(e instanceof ApiError) || e.status !== 502) throw e;
        });
        setParams({ session: session.id });
        return;
      }
      await api("POST", `/internal/onboarding/sessions/${session.id}/cancel`, {
        step: ev?.kind === "cancel" ? ev.currentStep ?? "" : "",
        error: ev?.kind === "error" ? ev.message : "",
      });
      setError(ev?.kind === "error" ? ev.message : ev?.kind === "finish" ? t("connect.noCode") : t("connect.popupClosed"));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  if (sessionId) return <Progress id={sessionId} onRestart={() => setParams({})} />;

  return (
    <section className="narrow">
      <h1>{t("connect.title")}</h1>
      <p className="muted">{t("connect.intro")}</p>
      {config.isSuccess && !configured && <div className="error">{t("connect.notConfigured")}</div>}
      <h2>{t("connect.chooseFlow")}</h2>
      <div className="choices">
        {(["standard", "coexistence"] as const).map((f) => (
          <label key={f} className={`choice card ${flow === f ? "selected" : ""}`}>
            <input type="radio" name="flow" checked={flow === f} onChange={() => setFlow(f)} />
            <div>
              <strong>{t(`connect.${f}`)}</strong>
              <div className="muted small">{t(`connect.${f}Hint`)}</div>
              {f === "coexistence" && <div className="muted small">{t("connect.coexistenceLimits")}</div>}
            </div>
          </label>
        ))}
      </div>
      {error && <div className="error">{error}</div>}
      <button className="primary" disabled={!sdkReady || busy} onClick={start}>
        {sdkReady || !configured || error ? t("connect.start") : t("connect.loadingSdk")}
      </button>
    </section>
  );
}

function Progress({ id, onRestart }: { id: string; onRestart: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [retryError, setRetryError] = useState("");
  const q = useQuery({
    queryKey: ["onboarding", id],
    queryFn: () => api<OnboardingSession>("GET", `/internal/onboarding/sessions/${id}`),
    // Poll while the worker runs the steps; server-sent events replace this later.
    refetchInterval: (query) => (query.state.data?.state === "in_progress" ? 2000 : false),
  });
  const s = q.data;

  useEffect(() => {
    if (s?.state === "completed") void qc.invalidateQueries({ queryKey: ["phone-numbers"] });
  }, [s?.state, qc]);

  if (!s) return <div className="muted">{q.error ? (q.error as Error).message : t("common.loading")}</div>;

  const steps = STEPS[s.flow];
  const reached = s.state === "completed" ? steps.length : steps.indexOf(s.step) + 1;

  const retry = async () => {
    setRetryError("");
    try {
      qc.setQueryData(["onboarding", id], await api<OnboardingSession>("POST", `/internal/onboarding/sessions/${id}/retry`));
    } catch (e) {
      setRetryError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  return (
    <section className="narrow">
      <h1>{s.state === "completed" ? t("connect.doneTitle") : t("connect.progressTitle")}</h1>
      <ol className="steps">
        {steps.map((step, i) => {
          const cls = i < reached ? "done" : i === reached ? (s.state === "failed" ? "failed" : "current") : "";
          return (
            <li key={step} className={cls}>
              {t(`connect.steps.${step}`)}
            </li>
          );
        })}
      </ol>
      {s.error && <div className="error">{s.error.message}</div>}
      {retryError && <div className="error">{retryError}</div>}
      <div className="actions">
        {s.state === "completed" && <Link className="button primary" to="/numbers">{t("connect.viewNumbers")}</Link>}
        {s.state === "failed" && s.step !== "code_received" && (
          <button className="primary" onClick={retry}>{t("connect.retry")}</button>
        )}
        {(s.state === "failed" || s.state === "cancelled") && (
          <button onClick={onRestart}>{t("connect.startOver")}</button>
        )}
      </div>
    </section>
  );
}
