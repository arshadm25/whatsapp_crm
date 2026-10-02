import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useMe, usePhoneNumbers } from "../api/hooks";
import Icon, { type IconName } from "../components/Icon";

const RING = 2 * Math.PI * 40;

export default function GetStarted() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const numbers = usePhoneNumbers();
  const connected = !!numbers.data?.some((n) => n.status === "connected");

  const steps = [
    { key: "stepAccount", done: true, hint: "stepAccountHint" },
    { key: "stepVerify", done: me.user.email_verified, hint: "stepVerifyHint" },
    { key: "stepConnect", done: connected, hint: "stepConnectHint", action: <Link className="button primary sm" to="/numbers/connect">{t("getStarted.connect")}</Link> },
  ];
  const done = steps.filter((s) => s.done).length;
  const current = steps.findIndex((s) => !s.done);
  const pct = Math.round((done / steps.length) * 100);

  const features: { key: string; icon: IconName; tone: string }[] = [
    { key: "Inbox", icon: "inbox", tone: "" },
    { key: "Templates", icon: "file", tone: "bl" },
    { key: "Campaigns", icon: "megaphone", tone: "am" },
  ];
  const links: { key: string; icon: IconName; to: string }[] = [
    { key: "Inbox", icon: "inbox", to: "/inbox" },
    { key: "Api", icon: "code", to: "/developers" },
    { key: "Team", icon: "userPlus", to: "/settings" },
  ];

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("getStarted.title", { name: me.user.name })}</h1>
          <p className="sub">{t("getStarted.intro")}</p>
        </div>
      </div>
      <div className="split">
        <div className="stack">
          <div className="card progress-card">
            <svg width="96" height="96" viewBox="0 0 96 96" role="img" aria-label={t("getStarted.progressTitle", { done, total: steps.length })}>
              <circle className="ring-track" cx="48" cy="48" r="40" fill="none" strokeWidth="10" />
              <circle className="ring-fill" cx="48" cy="48" r="40" fill="none" strokeWidth="10" strokeLinecap="round"
                strokeDasharray={`${(RING * done) / steps.length} ${RING}`} transform="rotate(-90 48 48)" />
              <text className="ring-text" x="48" y="55" textAnchor="middle">{pct}%</text>
            </svg>
            <div style={{ flex: 1, minWidth: 220 }}>
              <h2>{current < 0 ? t("getStarted.allSet") : t("getStarted.progressTitle", { done, total: steps.length })}</h2>
              <p className="sub">{current < 0 ? t("getStarted.progressTextDone") : t("getStarted.progressText")}</p>
            </div>
            {!connected && (
              <Link className="button primary" to="/numbers/connect">{t("getStarted.connect")}<Icon name="arrowRight" size="s" /></Link>
            )}
          </div>
          <div className="card flush">
            <div className="chd"><h2>{t("getStarted.checklist")}</h2></div>
            <ol className="checklist">
              {steps.map((s, i) => (
                <li key={s.key} className={s.done ? "done" : i === current ? "current" : ""}>
                  <span className="dot">{s.done ? <Icon name="check" size="s" /> : i + 1}</span>
                  <span className="tx">
                    <b>{t(`getStarted.${s.key}`)}</b>
                    <span>{t(`getStarted.${s.hint}`)}</span>
                  </span>
                  {s.done ? <span className="pill s-connected">{t("getStarted.done")}</span> : s.action}
                </li>
              ))}
            </ol>
          </div>
          <div className="grid g3">
            {features.map((f) => (
              <div className="card feature" key={f.key}>
                <span className={`ic ${f.tone}`}><Icon name={f.icon} /></span>
                <b>{t(`getStarted.feat${f.key}`)}</b>
                <span className="muted">{t(`getStarted.feat${f.key}Text`)}</span>
              </div>
            ))}
          </div>
        </div>
        <aside className="stack">
          <div className="card flush">
            <div className="chd"><h2>{t("getStarted.quickLinks")}</h2></div>
            {links.map((l) => (
              <Link className="ql" to={l.to} key={l.key}>
                <span className="ic gy"><Icon name={l.icon} size="s" /></span>
                <span className="t">
                  <b>{t(`getStarted.link${l.key}`)}</b>
                  <small>{t(`getStarted.link${l.key}Text`)}</small>
                </span>
                <Icon name="chevronRight" size="s" />
              </Link>
            ))}
          </div>
        </aside>
      </div>
    </section>
  );
}
