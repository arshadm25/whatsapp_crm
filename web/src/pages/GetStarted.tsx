import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useMe, usePhoneNumbers } from "../api/hooks";

export default function GetStarted() {
  const { t } = useTranslation();
  const me = useMe().data!;
  const numbers = usePhoneNumbers();
  const connected = !!numbers.data?.some((n) => n.status === "connected");

  const steps = [
    { key: "stepAccount", done: true },
    { key: "stepVerify", done: me.user.email_verified },
    { key: "stepConnect", done: connected, hint: "stepConnectHint", action: <Link className="button primary" to="/numbers/connect">{t("getStarted.connect")}</Link> },
  ];

  return (
    <section className="narrow">
      <h1>{t("getStarted.title", { name: me.user.name })}</h1>
      <p className="muted">{t("getStarted.intro")}</p>
      <ol className="checklist">
        {steps.map((s) => (
          <li key={s.key} className={`card ${s.done ? "done" : ""}`}>
            <div>
              <strong>{t(`getStarted.${s.key}`)}</strong>
              {s.hint && !s.done && <div className="muted small">{t(`getStarted.${s.hint}`)}</div>}
            </div>
            {s.done ? <span className="pill s-connected">{t("getStarted.done")}</span> : s.action}
          </li>
        ))}
      </ol>
    </section>
  );
}
