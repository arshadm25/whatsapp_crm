import { useEffect, useState, type ChangeEvent, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useConfig, usePhoneNumbers } from "../api/hooks";
import type { OnboardingFlow, OnboardingSession } from "../api/types";
import { launchEmbeddedSignup, loadFacebookSdk } from "../lib/embeddedSignup";
import Icon from "../components/Icon";
import { shortTime } from "../lib/time";

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
  const numbers = usePhoneNumbers();
  const [params, setParams] = useSearchParams();
  const sessionId = params.get("session");
  const [sdkReady, setSdkReady] = useState(false);
  const [busy, setBusy] = useState<OnboardingFlow | null>(null);
  const [error, setError] = useState("");

  const meta = config.data?.meta;
  const configured = !!(meta?.app_id && meta?.config_id);
  const connected = (numbers.data ?? []).filter((n) => n.status === "connected");

  // Load Meta's SDK up front: FB.login must run inside the click, or browsers block the popup.
  useEffect(() => {
    if (!configured || !meta) return;
    loadFacebookSdk(meta.app_id, meta.graph_api_version)
      .then(() => setSdkReady(true))
      .catch((e: Error) => setError(e.message));
  }, [configured, meta]);

  const start = async (flow: OnboardingFlow) => {
    setBusy(flow);
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
      // A non-API error here means Meta's sign-up script did not start (blocked or not ready).
      setError(e instanceof ApiError ? e.message : t("connect.sdkNotReady"));
    } finally {
      setBusy(null);
    }
  };

  const ready = sdkReady && !busy;
  const label = (flow: OnboardingFlow, text: string) => (busy === flow ? t("connect.loadingSdk") : text);

  return (
    <section>
      <div className="page-head">
        <div>
          <div className="crumb"><Link to="/numbers">{t("numbers.title")}</Link><Icon name="chevronRight" />{t("connect.crumb")}</div>
          <h1>{t("connect.title")}</h1>
          <p className="sub">{t("connect.intro")}</p>
        </div>
      </div>
      {config.isSuccess && !configured && <div className="error">{t("connect.notConfigured")}</div>}
      {error && <div className="error">{error}</div>}
      <div className="split">
        <div className="stack">
          {sessionId ? (
            <Progress id={sessionId} onRestart={() => setParams({})} />
          ) : (
            <>
            <div className="grid g2">
              <div className="card choice-card">
                <div className="row-between"><span className="ic lg"><Icon name="phone" /></span><span className="chip">{t("connect.recommended")}</span></div>
                <div><h2>{t("connect.standard")}</h2><p className="sub">{t("connect.standardHint")}</p></div>
                <ul className="lst">
                  {[1, 2, 3].map((i) => <li key={i}><Icon name="check" size="s" />{t(`connect.standardPoint${i}`)}</li>)}
                </ul>
                <button className="primary" disabled={!ready || !configured} onClick={() => start("standard")}>
                  <Icon name="facebook" size="s" />{label("standard", t("connect.start"))}
                </button>
              </div>
              <div className="card choice-card">
                <div><span className="ic lg bl"><Icon name="message" /></span></div>
                <div><h2>{t("connect.coexistence")}</h2><p className="sub">{t("connect.coexistenceHint")}</p></div>
                <ul className="lst">
                  {[1, 2, 3].map((i) => <li key={i}><Icon name="check" size="s" />{t(`connect.coexistencePoint${i}`)}</li>)}
                </ul>
                <span className="hint">{t("connect.coexistenceLimits")}</span>
                <button className="ghost" disabled={!ready || !configured} onClick={() => start("coexistence")}>
                  <Icon name="facebook" size="s" />{label("coexistence", t("connect.startExisting"))}
                </button>
              </div>
            </div>
            <TokenConnect onStarted={(id) => setParams({ session: id })} />
            </>
          )}
        </div>
        <aside className="stack">
          <div className="card flush">
            <div className="chd"><h2>{t("connect.needTitle")}</h2></div>
            <ul className="lst cb">
              {[1, 2, 3].map((i) => <li key={i}><Icon name="check" size="s" />{t(`connect.need${i}`)}</li>)}
            </ul>
          </div>
          <div className="card flush">
            <div className="chd"><h2>{t("connect.connectedTitle")}</h2><span className="chip gy">{connected.length}</span></div>
            {connected.length === 0 && <div className="cb muted">{t("numbers.empty")}</div>}
            {connected.slice(0, 3).map((n) => (
              <div key={n.id} className="num-row">
                <span className="ic"><Icon name="phone" size="s" /></span>
                <div><b>{n.display_phone_number}</b><span className="muted small">{n.verified_name ?? "—"}</span></div>
                <span className="pill ok">{t("numbers.status_connected")}</span>
              </div>
            ))}
            <div className="cf"><Link to="/numbers">{t("connect.manageNumbers")}<Icon name="chevronRight" size="xs" /></Link></div>
          </div>
        </aside>
      </div>
    </section>
  );
}

// Connects a number in the workspace's own Meta business with a system user token, for use
// before App Review grants the Advanced Access that Embedded Signup needs.
function TokenConnect({ onStarted }: { onStarted: (id: string) => void }) {
  const { t } = useTranslation();
  const [form, setForm] = useState({ waba_id: "", phone_number_id: "", access_token: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const set = (k: keyof typeof form) => (e: ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value.trim() });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const session = await api<OnboardingSession>("POST", "/internal/onboarding/sessions", { flow: "standard" });
      try {
        await api("POST", `/internal/onboarding/sessions/${session.id}/complete-with-token`, form);
      } catch (err) {
        // The session records Meta's refusal; anything else is shown here.
        if (!(err instanceof ApiError) || err.code !== "token_rejected") throw err;
      }
      onStarted(session.id);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <details className="card">
      <summary><b>{t("connect.tokenTitle")}</b></summary>
      <form className="form stack" onSubmit={submit} style={{ marginTop: 12 }}>
        <p className="sub">{t("connect.tokenHint")}</p>
        <label className="field">{t("connect.tokenWaba")}<input value={form.waba_id} onChange={set("waba_id")} required pattern="[0-9]{1,32}" inputMode="numeric" /></label>
        <label className="field">{t("connect.tokenPhone")}<input value={form.phone_number_id} onChange={set("phone_number_id")} required pattern="[0-9]{1,32}" inputMode="numeric" /></label>
        <label className="field">{t("connect.tokenToken")}<input type="password" autoComplete="off" value={form.access_token} onChange={set("access_token")} required maxLength={2048} /></label>
        {error && <div className="error">{error}</div>}
        <div className="actions"><button className="primary" disabled={busy}>{busy ? t("common.loading") : t("connect.tokenSubmit")}</button></div>
      </form>
    </details>
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

  if (!s) return <div className="card muted">{q.error ? (q.error as Error).message : t("common.loading")}</div>;

  const steps = STEPS[s.flow];
  const reached = s.state === "completed" ? steps.length : steps.indexOf(s.step) + 1;
  const pct = Math.round((reached / steps.length) * 100);

  const retry = async () => {
    setRetryError("");
    try {
      qc.setQueryData(["onboarding", id], await api<OnboardingSession>("POST", `/internal/onboarding/sessions/${id}/retry`));
    } catch (e) {
      setRetryError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  const pill = s.state === "completed" ? <span className="pill ok">{t("connect.stateDone")}</span>
    : s.state === "failed" ? <span className="pill er">{t("connect.stateFailed")}</span>
    : s.state === "cancelled" ? <span className="pill">{t("connect.stateCancelled")}</span>
    : <span className="pill wa">{t("connect.stateRunning")}</span>;

  return (
    <>
      {s.state === "failed" && s.error && (
        <div className="banner danger" style={{ margin: 0 }}>
          <Icon name="alert" size="s" />
          <div><b>{t("connect.stepFailed", { step: t(`connect.steps.${s.step}`) })}</b><span>{s.error.message}</span></div>
          {s.step !== "code_received" && <button className="sm" onClick={retry}><Icon name="refresh" size="xs" />{t("connect.retry")}</button>}
        </div>
      )}
      {retryError && <div className="error" style={{ margin: 0 }}>{retryError}</div>}
      <div className="card flush">
        <div className="chd">
          <div>
            <h2>{s.state === "completed" ? t("connect.doneTitle") : t("connect.progressTitle")}</h2>
            <p>{t("connect.startedAt", { time: shortTime(s.created_at), step: Math.min(reached + (s.state === "completed" ? 0 : 1), steps.length), total: steps.length })}</p>
          </div>
          {pill}
        </div>
        <div className="cb stack" style={{ gap: 20 }}>
          <div className="bar" style={{ height: 8 }}><span className={s.state === "failed" ? "rd" : ""} style={{ width: `${pct}%` }} /></div>
          <div className="steps-grid">
            {steps.map((step, i) => {
              const cls = i < reached ? "done" : i === reached ? (s.state === "failed" ? "failed" : s.state === "in_progress" ? "current" : "") : "";
              const state = cls === "done" ? t("connect.stepDone") : cls === "failed" ? t("connect.stepFailedShort") : cls === "current" ? t("connect.stepRunning") : t("connect.stepWaiting");
              return (
                <div key={step} className={cls}>
                  <span className="dot">{cls === "done" ? <Icon name="check" size="xs" /> : cls === "failed" ? <Icon name="x" size="xs" /> : i + 1}</span>
                  <b>{t(`connect.steps.${step}`)}</b>
                  <span className="muted">
                    {cls === "done" && s.step_times?.[step]
                      ? t("connect.doneAt", { time: new Date(s.step_times[step]).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) })
                      : state}
                  </span>
                </div>
              );
            })}
          </div>
          <div className="actions">
            {s.state === "completed" && <Link className="button primary" to="/numbers">{t("connect.viewNumbers")}</Link>}
            {(s.state === "failed" || s.state === "cancelled") && (
              <button onClick={onRestart}>{t("connect.startOver")}</button>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
