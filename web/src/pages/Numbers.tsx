import { useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers, useTemplates } from "../api/hooks";
import NumberProfile from "./NumberProfile";
import Icon from "../components/Icon";
import type { PhoneNumber } from "../api/types";
import { ago } from "../lib/time";
import { TIERS, highestTier, tierLabel, tierValue } from "../lib/numbers";

const QUALITY_ORDER: PhoneNumber["quality_rating"][] = ["red", "yellow", "green", "unknown"];
const QUALITY_PILL: Record<PhoneNumber["quality_rating"], string> = { green: "ok", yellow: "wa", red: "er", unknown: "" };
const STATUS_PILL: Record<PhoneNumber["status"], string> = { connected: "ok", pending: "wa", disconnected: "", revoked: "er", error: "er" };

export default function Numbers() {
  const { t } = useTranslation();
  const q = usePhoneNumbers();
  const templates = useTemplates();
  const qc = useQueryClient();
  const role = useMe().data?.tenant?.role;
  const manager = role === "owner" || role === "admin";
  const [editing, setEditing] = useState<string | null>(null);
  const profileCard = useRef<HTMLDivElement>(null);
  // The profile card sits below the table, already showing the first number, so the button
  // must bring it into view or a click looks like it did nothing.
  const editProfile = (id: string) => {
    setEditing(id);
    requestAnimationFrame(() => profileCard.current?.scrollIntoView({ behavior: "smooth", block: "start" }));
  };
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const sync = useMutation({
    mutationFn: () => api<{ synced: number }>("POST", "/internal/numbers/sync"),
    onSuccess: (r) => {
      setError(null);
      setNote(t("numbers.synced", { count: r.synced }));
      void qc.invalidateQueries({ queryKey: ["phone-numbers"] });
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : t("common.error")),
  });
  const disconnect = useMutation({
    mutationFn: (id: string) => api("POST", `/internal/numbers/${id}/disconnect`),
    onSuccess: () => {
      setError(null);
      void qc.invalidateQueries({ queryKey: ["phone-numbers"] });
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : t("common.error")),
  });
  const live = q.data?.filter((n) => n.status === "connected") ?? [];
  const editingNumber = q.data?.find((n) => n.id === editing) ?? live[0];
  const lowest = QUALITY_ORDER.find((r) => live.some((n) => n.quality_rating === r));
  const approved = templates.data?.filter((tp) => tp.status === "approved").length ?? 0;
  const pending = templates.data?.filter((tp) => tp.status === "pending").length ?? 0;
  const lastSync = (q.data ?? []).map((n) => n.last_synced_at).filter((x): x is string => !!x).sort().pop();
  const topTier = highestTier(live.map((n) => n.messaging_limit_tier));
  const lowQuality = live.filter((n) => n.quality_dropped || n.quality_rating === "yellow" || n.quality_rating === "red");

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("numbers.title")}</h1>
          <p className="sub">{t("numbers.intro")}</p>
        </div>
        <div className="actions">
          {manager && !!q.data?.length && (
            <button onClick={() => sync.mutate()} disabled={sync.isPending}>
              <Icon name="refresh" size="s" />{sync.isPending ? t("numbers.syncing") : t("numbers.sync")}
            </button>
          )}
          <Link className="button primary" to="/numbers/connect"><Icon name="plus" size="s" />{t("numbers.connect")}</Link>
        </div>
      </div>
      {note && <div className="banner ok"><Icon name="check" size="s" /><div><span>{note}</span></div></div>}
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
                <th>{t("numbers.status")}</th>
                <th>{t("numbers.quality")}</th>
                <th>{t("numbers.limit")}</th>
                <th>{t("numbers.limitUsed")}</th>
                <th>{t("numbers.registration")}</th>
                <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
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
                        <small>{n.is_coexistence ? t("numbers.coexistence") : n.id === live[0]?.id ? t("numbers.defaultSender") : "—"}</small>
                      </span>
                    </div>
                  </td>
                  <td>
                    {n.verified_name ?? "—"}
                    {n.name_status && (
                      <span className={`pill ${n.name_status === "APPROVED" ? "ok" : n.name_status === "DECLINED" ? "er" : "wa"}`} style={{ marginInlineStart: 6 }}>
                        {t(`numbers.name_${n.name_status}`, { defaultValue: n.name_status.toLowerCase().replace(/_/g, " ") })}
                      </span>
                    )}
                  </td>
                  <td><span className={`pill ${STATUS_PILL[n.status]}`}>{t(`numbers.status_${n.status}`)}</span></td>
                  <td>{n.quality_rating === "unknown" && n.status !== "connected" ? <span className="muted">—</span> : <span className={`pill ${QUALITY_PILL[n.quality_rating]}`}>{t(`numbers.quality_${n.quality_rating}`)}</span>}</td>
                  <td className="num-t">{n.messaging_limit_tier ? tierLabel(n.messaging_limit_tier, t("numbers.unlimited")) : <span className="muted">—</span>}</td>
                  <td className="num-t">
                    {n.limit_used_today === undefined || n.daily_limit === undefined
                      ? <span className="muted">—</span>
                      : n.daily_limit < 0
                        ? t("numbers.usedUnlimited", { used: n.limit_used_today.toLocaleString() })
                        : `${n.limit_used_today.toLocaleString()} / ${n.daily_limit.toLocaleString()}`}
                  </td>
                  <td>
                    {n.registered_at
                      ? <span className="pill ok" title={new Date(n.registered_at).toLocaleString()}>{t("numbers.registered")}</span>
                      : <span className="pill wa">{t("numbers.notRegistered")}</span>}
                  </td>
                  <td className="r">
                    {manager && n.status === "connected" && (
                      <span className="actions" style={{ justifyContent: "flex-end" }}>
                        <button className="sm" onClick={() => editProfile(n.id)}>{t("numbers.editProfile")}</button>
                        <button
                          className="sm bdg"
                          disabled={disconnect.isPending}
                          onClick={() => {
                            if (window.confirm(t("numbers.disconnectConfirm", { number: n.display_phone_number }))) disconnect.mutate(n.id);
                          }}
                        >
                          {t("numbers.disconnect")}
                        </button>
                      </span>
                    )}
                    {manager && (n.status === "disconnected" || n.status === "error" || n.status === "revoked") && (
                      <Link className="button sm ghost" to="/numbers/connect">{t("numbers.reconnect")}</Link>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {(editingNumber || topTier) && (
        <div className="split" style={{ marginTop: 16 }}>
          <div className="stack" ref={profileCard}>
            {editingNumber && <NumberProfile key={editingNumber.id} number={editingNumber} canEdit={manager} />}
          </div>
          <aside className="stack">
            {topTier && (
              <div className="card flush">
                <div className="chd"><div><h2>{t("numbers.tiersTitle")}</h2><p>{t("numbers.tiersHint")}</p></div></div>
                <ol className="tiers">
                  {TIERS.map((tier) => (
                    <li key={tier} className={tier === topTier ? "current" : tierValue(tier) < tierValue(topTier) ? "passed" : ""}>
                      <span className="td" />
                      <b>{tierLabel(tier, t("numbers.unlimited"))}</b>
                      {tier !== "TIER_UNLIMITED" && <span className="muted">{t("numbers.statLimitHint")}</span>}
                      {tier === topTier && <span className="chip">{t("numbers.current")}</span>}
                    </li>
                  ))}
                </ol>
              </div>
            )}
            {lowQuality.map((n) => (
              <div key={n.id} className="banner" style={{ margin: 0 }}>
                <Icon name="alert" size="s" />
                <div>
                  <b>
                    {n.quality_dropped && n.previous_quality_rating
                      ? t("numbers.qualityDropped", {
                          number: n.display_phone_number,
                          from: t(`numbers.quality_${n.previous_quality_rating}`),
                          to: t(`numbers.quality_${n.quality_rating}`),
                        })
                      : t("numbers.qualityAlert", { number: n.display_phone_number, rating: t(`numbers.quality_${n.quality_rating}`) })}
                  </b>
                  <span>{t("numbers.qualityAlertHint")}</span>
                </div>
              </div>
            ))}
          </aside>
        </div>
      )}
    </section>
  );
}
