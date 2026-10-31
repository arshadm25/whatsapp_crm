import { useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers, useTemplates } from "../api/hooks";
import Icon from "../components/Icon";
import NumbersHead from "./numbers/NumbersHead";
import QualityAlerts from "./numbers/QualityAlerts";
import type { PhoneNumber } from "../api/types";
import { ago } from "../lib/time";
import { highestTier, registrationState, tierLabel, usageLevel, usagePct } from "../lib/numbers";

const QUALITY_ORDER: PhoneNumber["quality_rating"][] = ["red", "yellow", "green", "unknown"];
export const QUALITY_PILL: Record<PhoneNumber["quality_rating"], string> = { green: "ok", yellow: "wa", red: "er", unknown: "" };
const STATUS_PILL: Record<PhoneNumber["status"], string> = { connected: "ok", pending: "wa", disconnected: "", revoked: "er", error: "er" };
const REGISTRATION_PILL = { registered: "ok", businessApp: "ok", notRegistered: "wa", none: "" } as const;

// Numbers > Numbers: the numbers on the workspace's WhatsApp Business Accounts and their health.
// The business profile and messaging limits have their own tabs.
export default function Numbers() {
  const { t } = useTranslation();
  const q = usePhoneNumbers();
  const templates = useTemplates();
  const qc = useQueryClient();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const [error, setError] = useState<string | null>(null);
  const disconnect = useMutation({
    mutationFn: (id: string) => api("POST", `/internal/numbers/${id}/disconnect`),
    onSuccess: () => {
      setError(null);
      void qc.invalidateQueries({ queryKey: ["phone-numbers"] });
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : t("common.error")),
  });
  // Disconnected numbers are history, not something to manage, so the page leaves them out.
  const numbers = q.data?.filter((n) => n.status !== "disconnected");
  const live = numbers?.filter((n) => n.status === "connected") ?? [];
  const lowest = QUALITY_ORDER.find((r) => live.some((n) => n.quality_rating === r));
  const approved = templates.data?.filter((tp) => tp.status === "approved").length ?? 0;
  const pending = templates.data?.filter((tp) => tp.status === "pending").length ?? 0;
  const lastSync = (numbers ?? []).map((n) => n.last_synced_at).filter((x): x is string => !!x).sort().pop();
  const topTier = highestTier(live.map((n) => n.messaging_limit_tier));
  const regs = (numbers ?? []).map(registrationState);

  return (
    <section>
      <NumbersHead />
      {!!numbers?.length && (
        <div className="grid g4">
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statConnected")}</span><span className="ic"><Icon name="phone" size="s" /></span></div>
            <span className="sv">{live.length}</span>
            <span className="sf">{t("numbers.statOf", { count: numbers.length })}</span>
          </div>
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statQuality")}</span><span className="ic"><Icon name="shield" size="s" /></span></div>
            <span className="sv">{lowest ? t(`numbers.quality_${lowest}`) : "—"}</span>
            <span className="sf">{t("numbers.quality")}</span>
          </div>
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statLimit")}</span><span className="ic bl"><Icon name="chart" size="s" /></span></div>
            <span className="sv">{tierLabel(topTier, t("numbers.unlimited"))}</span>
            <span className="sf">{t("numbers.statLimitHint")}</span>
          </div>
          <div className="card stat">
            <div className="sh"><span className="sl">{t("numbers.statTemplates")}</span><span className="ic am"><Icon name="file" size="s" /></span></div>
            <span className="sv">{approved}</span>
            <span className="sf">{t("numbers.statPending", { count: pending })}</span>
          </div>
        </div>
      )}
      <QualityAlerts numbers={live} />
      {error && <div className="field-error">{error}</div>}
      {q.isLoading && <div className="muted">{t("common.loading")}</div>}
      {numbers?.length === 0 && <div className="card muted">{t("numbers.empty")}</div>}
      {!!numbers?.length && (
        <div className="card table-wrap numbers-table">
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
                <th>{t("numbers.status")}</th>
                <th>{t("numbers.quality")}</th>
                <th>{t("numbers.limitUsed")}</th>
                <th>{t("numbers.registration")}</th>
                <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {numbers.map((n, i) => {
                const reg = regs[i];
                return (
                  <tr key={n.id}>
                    <td>
                      <div className="who">
                        <span className={n.status === "connected" ? "ic" : "ic gy"}><Icon name="phone" size="s" /></span>
                        <span className="num-cell">
                          <b className="nowrap">{n.display_phone_number}</b>
                          <small>
                            {n.verified_name ?? t("numbers.noName")}
                            {n.name_status && (
                              <span className={`pill ${n.name_status === "APPROVED" || n.name_status === "AVAILABLE_WITHOUT_REVIEW" ? "ok" : n.name_status === "DECLINED" ? "er" : "wa"}`} title={t("numbers.nameStatusHint")}>
                                {t(`numbers.name_${n.name_status}`, { defaultValue: n.name_status.toLowerCase().replace(/_/g, " ") })}
                              </span>
                            )}
                          </small>
                          <small>{n.is_coexistence ? t("numbers.coexistenceShort") : n.id === live[0]?.id ? t("numbers.defaultSender") : t("numbers.cloudApi")}</small>
                        </span>
                      </div>
                    </td>
                    <td><span className={`pill ${STATUS_PILL[n.status]}`}>{t(`numbers.status_${n.status}`)}</span></td>
                    <td>{n.quality_rating === "unknown" && n.status !== "connected" ? <span className="muted">—</span> : <span className={`pill ${QUALITY_PILL[n.quality_rating]}`}>{t(`numbers.quality_${n.quality_rating}`)}</span>}</td>
                    <td className="num-t">
                      {n.limit_used_today === undefined || n.daily_limit === undefined ? (
                        <span className="muted">—</span>
                      ) : n.daily_limit < 0 ? (
                        t("numbers.usedUnlimited", { used: n.limit_used_today.toLocaleString() })
                      ) : (
                        <div className="usage-cell" title={t("numbers.limitHint")}>
                          <span className="nowrap">{t("numbers.usedOf", { used: n.limit_used_today.toLocaleString(), limit: n.daily_limit.toLocaleString() })}</span>
                          <span className={`meter ${usageLevel(n.limit_used_today, n.daily_limit) === "ok" ? "" : usageLevel(n.limit_used_today, n.daily_limit)}`}>
                            <span style={{ width: `${usagePct(n.limit_used_today, n.daily_limit)}%` }} />
                          </span>
                        </div>
                      )}
                    </td>
                    <td>
                      {reg === "none" ? (
                        <span className="muted">—</span>
                      ) : (
                        <span
                          className={`pill ${REGISTRATION_PILL[reg]}`}
                          title={reg === "registered" ? new Date(n.registered_at!).toLocaleString() : t(`numbers.reg_${reg}Hint`)}
                        >
                          {t(`numbers.reg_${reg}`)}
                        </span>
                      )}
                    </td>
                    <td className="r">
                      {/* Pending and errored numbers can be disconnected too: a pending one still takes a
                          plan slot, and nothing else on this page frees it. */}
                      <span className="actions nowrap" style={{ justifyContent: "flex-end", flexWrap: "nowrap" }}>
                        {n.status === "connected" && (
                          <Link className="button sm" to={`/numbers/profile?number=${n.id}`}>{t("numbers.editProfile")}</Link>
                        )}
                        {manager && (n.status === "connected" || n.status === "pending" || n.status === "error") && (
                          <button
                            className="sm bdg"
                            disabled={disconnect.isPending}
                            onClick={() => {
                              if (window.confirm(t("numbers.disconnectConfirm", { number: n.display_phone_number }))) disconnect.mutate(n.id);
                            }}
                          >
                            {t("numbers.disconnect")}
                          </button>
                        )}
                        {manager && (n.status === "error" || n.status === "revoked") && (
                          <Link className="button sm ghost" to="/numbers/connect">{t("numbers.reconnect")}</Link>
                        )}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          <dl className="numbers-legend">
            <div>
              <dt>{t("numbers.limitUsed")}</dt>
              <dd>{t("numbers.limitHint")} <Link to="/numbers/limits">{t("numbers.limitMore")}</Link></dd>
            </div>
            <div>
              <dt>{t("numbers.registration")}</dt>
              <dd>
                {regs.includes("businessApp") ? t("numbers.reg_businessAppHint") + " " : ""}
                {regs.includes("notRegistered") ? t("numbers.reg_notRegisteredHint") : !regs.includes("businessApp") ? t("numbers.reg_registeredHint") : ""}
              </dd>
            </div>
          </dl>
        </div>
      )}
    </section>
  );
}
