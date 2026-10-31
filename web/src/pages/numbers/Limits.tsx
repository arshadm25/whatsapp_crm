import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { usePhoneNumbers } from "../../api/hooks";
import { TIERS, highestTier, tierLabel, tierValue, usageLevel, usagePct } from "../../lib/numbers";
import { QUALITY_PILL } from "../Numbers";
import NumbersHead from "./NumbersHead";
import QualityAlerts from "./QualityAlerts";

// Numbers > Limits and quality: today's use of each number's messaging limit, Meta's tiers, and
// what quality means for them.
export default function Limits() {
  const { t } = useTranslation();
  const q = usePhoneNumbers();
  const live = q.data?.filter((n) => n.status === "connected") ?? [];
  const topTier = highestTier(live.map((n) => n.messaging_limit_tier));

  return (
    <section>
      <NumbersHead />
      <QualityAlerts numbers={live} />
      {q.isLoading && <div className="muted">{t("common.loading")}</div>}
      {q.data && !live.length && <div className="card muted">{t("numbers.limitsNone")}</div>}
      {!!live.length && (
        <div className="split">
          <div className="stack">
            <div className="card">
              <h2>{t("numbers.usageTitle")}</h2>
              <p className="muted small">{t("numbers.limitHint")}</p>
              <ul className="meters">
                {live.map((n) => {
                  const used = n.limit_used_today ?? 0;
                  const limit = n.daily_limit;
                  const level = limit === undefined ? "ok" : usageLevel(used, limit);
                  return (
                    <li key={n.id}>
                      <div className="meter-head">
                        <span>
                          <b className="nowrap">{n.display_phone_number}</b>{" "}
                          <span className={`pill ${QUALITY_PILL[n.quality_rating]}`}>{t(`numbers.quality_${n.quality_rating}`)}</span>
                        </span>
                        <span className={`meter-count ${level === "ok" ? "" : level}`}>
                          {limit === undefined
                            ? "—"
                            : limit < 0
                              ? t("numbers.usedUnlimited", { used: used.toLocaleString() })
                              : t("numbers.usedOf", { used: used.toLocaleString(), limit: limit.toLocaleString() })}
                        </span>
                      </div>
                      {limit !== undefined && limit >= 0 && (
                        <div className={`meter ${level === "ok" ? "" : level}`}><span style={{ width: `${usagePct(used, limit)}%` }} /></div>
                      )}
                      {level !== "ok" && <p className="hint">{t(level === "full" ? "numbers.limitFull" : "numbers.limitNear")}</p>}
                    </li>
                  );
                })}
              </ul>
            </div>
            <div className="card">
              <h2>{t("numbers.limitsHowTitle")}</h2>
              <ul className="plain-list">
                <li>{t("numbers.limitsHowCounts")}</li>
                <li>{t("numbers.limitsHowFree")}</li>
                <li>{t("numbers.limitsHowRaise")}</li>
                <li>{t("numbers.limitsHowQuality")}</li>
              </ul>
              <p className="muted small">
                {t("numbers.limitsHowSource")}{" "}
                <a href="https://developers.facebook.com/docs/whatsapp/messaging-limits" target="_blank" rel="noreferrer">{t("numbers.limitsMetaDocs")}</a>
              </p>
            </div>
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
            <p className="muted small">
              <Link to="/numbers">{t("numbers.backToNumbers")}</Link>
            </p>
          </aside>
        </div>
      )}
    </section>
  );
}
