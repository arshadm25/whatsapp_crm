import type { FormEvent, ReactNode } from "react";

export function AuthCard({ title, children, onSubmit, footer }: {
  title: string;
  children: ReactNode;
  onSubmit: (e: FormEvent) => void;
  footer: ReactNode;
}) {
  return (
    <div className="auth-page">
      <form className="card auth-card" onSubmit={onSubmit}>
        <div className="brand">Ecogo WhatsApp</div>
        <h1>{title}</h1>
        {children}
        <div className="auth-foot">{footer}</div>
      </form>
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
