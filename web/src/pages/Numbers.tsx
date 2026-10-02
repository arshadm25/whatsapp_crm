import { useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers, useTemplates } from "../api/hooks";
import NumberProfile from "./NumberProfile";
import Icon from "../components/Icon";
import type { PhoneNumber } from "../api/types";

const QUALITY_ORDER: PhoneNumber["quality_rating"][] = ["red", "yellow", "green", "unknown"];

const TIERS = ["TIER_250", "TIER_1K", "TIER_2K", "TIER_10K", "TIER_100K", "TIER_UNLIMITED"];

function ago(iso: string, t: (k: string, o?: Record<string, unknown>) => string) {
  const mins = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60000));
  if (mins < 1) return t("numbers.justNow");
  if (mins < 60) return t("numbers.minutesAgo", { count: mins });
  if (mins < 48 * 60) return t("numbers.hoursAgo", { count: Math.round(mins / 60) });
  return new Date(iso).toLocaleDateString();
}

function tierValue(tier: string | null) {
  if (!tier) return 0;
  if (tier.includes("UNLIMITED")) return Infinity;
  const n = Number(tier.replace(/\D/g, ""));
  return tier.endsWith("K") ? n * 1000 : n;
}

export default function Numbers() {
  const { t } = useTranslation();
  const q = usePhoneNumbers();
  const templates = useTemplates();
  const qc = useQueryClient();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const [editing, setEditing] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const disconnect = useMutation({
    mutationFn: (id: string) => api("POST", `/internal/numbers/${id}/disconnect`),
    onSuccess: () => {
      setError(null);
      void qc.invalidateQueries({ queryKey: ["phone-numbers"] });
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : t("common.error")),
  });
  const editingNumber = q.data?.find((n) => n.id === editing);
  const live = q.data?.filter((n) => n.status === "connected") ?? [];
  const lowest = QUALITY_ORDER.find((r) => live.some((n) => n.quality_rating === r));
  const approved = templates.data?.filter((tp) => tp.status === "approved").length ?? 0;
  const pending = templates.data?.filter((tp) => tp.status === "pending").length ?? 0;
  const lastSync = (q.data ?? []).map((n) => n.last_synced_at).filter((x): x is string => !!x).sort().pop();
  const topTier = live.map((n) => n.messaging_limit_tier).sort((a, b) => tierValue(b) - tierValue(a))[0];

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("numbers.title")}</h1>
          <p className="sub">{t("numbers.intro")}</p>
        </div>
        <Link className="button primary" to="/numbers/connect">{t("numbers.connect")}</Link>
      </div>
      {!!q.data?.length && (
        <div className="grid g4" style={{ marginBottom: 16 }}>
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statConnected")}</span><span className="ic"><Icon name="phone" size="s" /></span></div>
            <span className="sv">{live.length}</span>
            <span className="sf">{t("numbers.statOf", { count: q.data.length })}</span>
          </div>
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statQuality")}</span><span className="ic"><Icon name="shield" size="s" /></span></div>
            <span className="sv">{lowest ? t(`numbers.quality_${lowest}`) : "—"}</span>
            <span className="sf">{t("numbers.quality")}</span>
          </div>
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statLimit")}</span><span className="ic bl"><Icon name="chart" size="s" /></span></div>
            <span className="sv">{topTier?.replace("TIER_", "") ?? "—"}</span>
            <span className="sf">{t("numbers.statLimitHint")}</span>
          </div>
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statTemplates")}</span><span className="ic am"><Icon name="file" size="s" /></span></div>
            <span className="sv">{approved}</span>
            <span className="sf">{t("numbers.statPending", { count: pending })}</span>
          </div>
        </div>
      )}
      {live.filter((n) => n.quality_rating === "yellow" || n.quality_rating === "red").map((n) => (
        <div key={n.id} className="banner">
          <b>{t("numbers.qualityAlert", { number: n.display_phone_number, rating: t(`numbers.quality_${n.quality_rating}`) })}</b>
          {t("numbers.qualityAlertHint")}
        </div>
      ))}
      {error && <div className="field-error">{error}</div>}
      {q.isLoading && <div className="muted">{t("common.loading")}</div>}
      {q.data?.length === 0 && <div className="card muted">{t("numbers.empty")}</div>}
      {!!q.data?.length && (
        <div className="card table-wrap">
          <div className="chd">
            <div>
              <h2>{t("numbers.tableTitle")}</h2>
              {lastSync && <p>{t("numbers.syncedAgo", { ago: ago(lastSync, t) })}</p>}
            </div>
          </div>
          <table>
            <thead>
              <tr>
                <th>{t("numbers.number")}</th>
                <th>{t("numbers.name")}</th>
                <th>{t("numbers.quality")}</th>
                <th>{t("numbers.limit")}</th>
                <th>{t("numbers.status")}</th>
                {manager && <th>{t("numbers.actions")}</th>}
              </tr>
            </thead>
            <tbody>
              {q.data.map((n) => (
                <tr key={n.id}>
                  <td>
                    <div className="who">
                      <span className={n.status === "connected" ? "ic" : "ic gy"}><Icon name="phone" size="s" /></span>
                      <span>
                        <b>{n.display_phone_number}</b>
                        {n.is_coexistence && <small>{t("numbers.coexistence")}</small>}
                      </span>
                    </div>
                  </td>
                  <td>
                    {n.verified_name ?? "—"}
                    {n.name_status && (
                      <span className={`pill ${n.name_status === "APPROVED" ? "q-green" : n.name_status === "DECLINED" ? "q-red" : "q-yellow"}`} style={{ marginInlineStart: 8 }}>
                        {t(`numbers.name_${n.name_status}`, { defaultValue: n.name_status.toLowerCase().replace(/_/g, " ") })}
                      </span>
                    )}
                  </td>
                  <td><span className={`pill q-${n.quality_rating}`}>{t(`numbers.quality_${n.quality_rating}`)}</span></td>
                  <td>{n.messaging_limit_tier?.replace("TIER_", "") ?? "—"}</td>
                  <td><span className={`pill s-${n.status}`}>{t(`numbers.status_${n.status}`)}</span></td>
                  {manager && (
                    <td>
                      {n.status === "connected" && (
                        <>
                          <button className="link" onClick={() => setEditing(n.id)}>{t("numbers.editProfile")}</button>{" "}
                          <button
                            className="link danger"
                            disabled={disconnect.isPending}
                            onClick={() => {
                              if (window.confirm(t("numbers.disconnectConfirm", { number: n.display_phone_number }))) disconnect.mutate(n.id);
                            }}
                          >
                            {t("numbers.disconnect")}
                          </button>
                        </>
                      )}
                      {(n.status === "disconnected" || n.status === "error" || n.status === "revoked") && (
                        <Link to="/numbers/connect">{t("numbers.reconnect")}</Link>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {topTier && (
        <div className="card flush" style={{ marginTop: 16, maxWidth: 520 }}>
          <div className="chd"><div><h2>{t("numbers.tiersTitle")}</h2><p>{t("numbers.tiersHint")}</p></div></div>
          <ol className="tiers">
            {TIERS.map((tier) => (
              <li key={tier} className={tier === topTier ? "current" : tierValue(tier) < tierValue(topTier) ? "passed" : ""}>
                <span className="td" />
                <b>{tier === "TIER_UNLIMITED" ? t("numbers.unlimited") : tier.replace("TIER_", "")}</b>
                {tier !== "TIER_UNLIMITED" && <span className="muted">{t("numbers.statLimitHint")}</span>}
                {tier === topTier && <span className="chip">{t("numbers.current")}</span>}
              </li>
            ))}
          </ol>
        </div>
      )}
      {editingNumber && <NumberProfile number={editingNumber} onClose={() => setEditing(null)} />}
    </section>
  );
}
