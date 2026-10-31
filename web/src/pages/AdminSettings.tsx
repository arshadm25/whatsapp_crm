import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import type { AdminTenant, PlatformSettings } from "../api/types";
import Icon from "../components/Icon";
import ReasonField from "../components/ReasonField";

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

// Percent text <-> basis points. Empty text means "use the default".
const pct = (bp: number) => String(bp / 100);
function toBP(s: string): number | null | undefined {
  const v = s.trim();
  if (v === "") return null;
  const n = Number(v);
  if (!Number.isFinite(n) || n < 0 || n > 100) return undefined;
  return Math.round(n * 100);
}

export function usePlatformSettings() {
  return useQuery({
    queryKey: ["admin", "settings"],
    queryFn: () => api<PlatformSettings>("GET", "/internal/admin/settings"),
  });
}

// AdminSettings is Admin > Settings: values that replace the deployment's, each one empty to go
// back to it. Every save asks for a reason kept in the audit log.
export default function AdminSettings() {
  const { t } = useTranslation();
  const settings = usePlatformSettings();
  const s = settings.data;
  if (!s) return <div className="card muted">{t("common.loading")}</div>;
  return <SettingsForm key={s.updated_at ?? "none"} s={s} />;
}

function SettingsForm({ s }: { s: PlatformSettings }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const initial = {
    gst: s.gst_rate_bp.overridden ? pct(s.gst_rate_bp.value) : "",
    markup: s.meta_markup_bp.overridden ? pct(s.meta_markup_bp.value) : "",
    model: s.ai_model.overridden ? s.ai_model.value : "",
  };
  const [f, setF] = useState(initial);
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const gst = toBP(f.gst);
  const markup = toBP(f.markup);
  const changed = f.gst !== initial.gst || f.markup !== initial.markup || f.model.trim() !== initial.model;

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setSaved(false);
    try {
      const out = await api<PlatformSettings>("PUT", "/internal/admin/settings", {
        gst_rate_bp: gst, meta_markup_bp: markup, ai_model: f.model.trim() || null, reason,
      });
      qc.setQueryData(["admin", "settings"], out);
      await qc.invalidateQueries({ queryKey: ["admin", "seller"] });
      setSaved(true);
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setSaved(false);
    setF({ ...f, [k]: e.target.value });
  };
  const state = (overridden: boolean) => (
    <span className={`pill ${overridden ? "ok" : ""}`}>{overridden ? t("admin.setHere") : t("admin.fromDeployment")}</span>
  );

  return (
    <form className="stack narrow" onSubmit={save}>
      {saved && <div className="banner ok"><Icon name="check" size="s" /><div><span>{t("admin.settingsSaved")}</span></div></div>}
      <div className="card form ws-action">
        <div className="page-head"><h3><Icon name="file" size="s" />{t("admin.gstRate")}</h3>{state(s.gst_rate_bp.overridden)}</div>
        <p className="muted small">{t("admin.gstRateHelp")}</p>
        <label className="field">
          {t("admin.gstRatePct")}
          <span className="pct-input">
            <input inputMode="decimal" value={f.gst} onChange={set("gst")} placeholder={pct(s.gst_rate_bp.deployment)} aria-invalid={gst === undefined} />
            <span>%</span>
          </span>
          <span className="hint">{t("admin.emptyUsesDeployment", { value: `${pct(s.gst_rate_bp.deployment)}%` })}</span>
        </label>
        <div className="banner"><Icon name="alert" size="s" /><div><span>{t("admin.gstRateWarn")}</span></div></div>
      </div>

      <div className="card form ws-action">
        <div className="page-head"><h3><Icon name="globe" size="s" />{t("admin.metaMarkup")}</h3>{state(s.meta_markup_bp.overridden)}</div>
        <p className="muted small">{t("admin.metaMarkupHelp")}</p>
        <label className="field">
          {t("admin.metaMarkupPct")}
          <span className="pct-input">
            <input inputMode="decimal" value={f.markup} onChange={set("markup")} placeholder={pct(s.meta_markup_bp.deployment)} aria-invalid={markup === undefined} />
            <span>%</span>
          </span>
          <span className="hint">{t("admin.emptyUsesDeployment", { value: `${pct(s.meta_markup_bp.deployment)}%` })}</span>
        </label>
      </div>

      <div className="card form ws-action">
        <div className="page-head"><h3><Icon name="sparkles" size="s" />{t("admin.aiModel")}</h3>{state(s.ai_model.overridden)}</div>
        <p className="muted small">{t("admin.aiModelHelp")}</p>
        <label className="field">
          {t("admin.aiModelId")}
          <input value={f.model} onChange={set("model")} placeholder={s.ai_model.deployment} maxLength={100} spellCheck={false} />
          <span className="hint">{t("admin.emptyUsesDeployment", { value: s.ai_model.deployment || "—" })}</span>
        </label>
        {!s.ai_configured && <div className="banner"><Icon name="alert" size="s" /><div><span>{t("admin.aiNoKey")}</span></div></div>}
      </div>

      <div className="card form">
        <ReasonField value={reason} onChange={setReason} />
        {(gst === undefined || markup === undefined) && <div className="field-error">{t("admin.pctInvalid")}</div>}
        {error && <div className="field-error">{error}</div>}
        <div className="actions">
          {!changed && <span className="hint">{t("admin.nothingChanged")}</span>}
          <button className="primary" disabled={!changed || gst === undefined || markup === undefined}>{t("admin.saveSettings")}</button>
        </div>
      </div>
    </form>
  );
}

// MarkupCard sets one workspace's own markup on Meta's fees; empty puts it back on the platform's.
export function MarkupCard({ id, own, payingThroughUs }: { id: string; own: AdminTenant["meta_markup_bp"]; payingThroughUs: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const platform = usePlatformSettings().data?.meta_markup_bp.value;
  const [v, setV] = useState(own === null ? "" : pct(own));
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const bp = toBP(v);
  const same = bp === own;
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("POST", `/internal/admin/tenants/${id}/meta-markup`, { markup_bp: bp, reason });
      setReason("");
      await qc.invalidateQueries({ queryKey: ["admin", "tenant", id] });
    } catch (err) {
      setError(message(err, t("common.error")));
    }
  };
  const platformText = platform === undefined ? "…" : `${pct(platform)}%`;
  return (
    <form className="card form ws-action" onSubmit={save}>
      <h3><Icon name="globe" size="s" />{t("admin.metaMarkup")}</h3>
      <p className="muted small">
        {own === null ? t("admin.markupUsesPlatform", { value: platformText }) : t("admin.markupOwn", { value: `${pct(own)}%`, platform: platformText })}
        {!payingThroughUs && <> {t("admin.markupNotUsed")}</>}
      </p>
      <label className="field">
        {t("admin.markupForWorkspace")}
        <span className="pct-input">
          <input inputMode="decimal" value={v} onChange={(e) => setV(e.target.value)} placeholder={platform === undefined ? "" : pct(platform)} aria-invalid={bp === undefined} />
          <span>%</span>
        </span>
        <span className="hint">{t("admin.emptyUsesPlatform")}</span>
      </label>
      <ReasonField value={reason} onChange={setReason} />
      {bp === undefined && <div className="field-error">{t("admin.pctInvalid")}</div>}
      {error && <div className="field-error">{error}</div>}
      <div className="actions">
        {same && <span className="hint">{t("admin.markupSame")}</span>}
        <button className="primary" disabled={same || bp === undefined}>{t("admin.markupSave")}</button>
      </div>
    </form>
  );
}
