import type { FormEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import Icon, { type IconName } from "../components/Icon";

const FEATURES: { key: string; icon: IconName }[] = [
  { key: "Connect", icon: "zap" },
  { key: "Team", icon: "users" },
  { key: "Templates", icon: "megaphone" },
];

export function AuthCard({ title, children, onSubmit, footer }: {
  title: string;
  children: ReactNode;
  onSubmit: (e: FormEvent) => void;
  footer: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="auth-page">
      <section className="auth-panel">
        <span className="ring" aria-hidden="true" style={{ width: 520, height: 520, right: -180, bottom: -200 }} />
        <span className="ring" aria-hidden="true" style={{ width: 340, height: 340, right: -90, bottom: -110 }} />
        <div className="logo-row">
          <img src="/ecogo-logo.webp" alt="Ecogo" />
          <span className="tag">{t("auth.panelTag")}</span>
        </div>
        <div>
          <h2>{t("auth.panelTitle")}</h2>
          <p className="lead">{t("auth.panelLead")}</p>
        </div>
        <div className="feats">
          {FEATURES.map((f) => (
            <div className="ft" key={f.key}>
              <span className="t"><Icon name={f.icon} /></span>
              <span>
                <b>{t(`auth.feat${f.key}`)}</b>
                <p>{t(`auth.feat${f.key}Text`)}</p>
              </span>
            </div>
          ))}
        </div>
        <p className="copy">© {new Date().getFullYear()} Ecogo Software Solutions</p>
      </section>
      <main className="auth-main">
        <form className="auth-card" onSubmit={onSubmit}>
          <h1>{title}</h1>
          {children}
          <div className="auth-foot">{footer}</div>
        </form>
      </main>
    </div>
  );
}

export function Field({ label, hint, error, ...input }: {
  label: string;
  hint?: string;
  error?: string;
} & React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="field">
      <span>{label}</span>
      <input {...input} aria-invalid={!!error} />
      {error ? <span className="field-error">{error}</span> : hint && <span className="muted small">{hint}</span>}
    </label>
  );
}
