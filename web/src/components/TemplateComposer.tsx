import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api, ApiError } from "../api/client";
import { useTemplates } from "../api/hooks";
import type { Message } from "../api/types";
import { bodyVariables } from "../lib/templates";

// Picks an approved template of the number's account, asks for its variables and sends it.
export default function TemplateComposer({
  phoneNumberId,
  accountId,
  to,
  onSent,
}: {
  phoneNumberId: string;
  accountId: string | undefined;
  to: string;
  onSent: (m: Message) => void;
}) {
  const { t } = useTranslation();
  const templates = useTemplates("approved");
  const usable = (templates.data ?? []).filter((tp) => tp.whatsapp_account_id === accountId);
  const [templateId, setTemplateId] = useState("");
  const [params, setParams] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const template = usable.find((tp) => tp.id === templateId) ?? usable[0];
  const vars = template ? bodyVariables(template.components) : [];

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!template) return;
    setBusy(true);
    setError("");
    try {
      const m = await api<Message>("POST", "/v1/messages", {
        phone_number_id: phoneNumberId,
        to,
        type: "template",
        template: {
          name: template.name,
          language: template.language,
          ...(vars.length && {
            components: [{ type: "body", parameters: vars.map((_, i) => ({ type: "text", text: params[i] ?? "" })) }],
          }),
        },
      });
      setParams([]);
      onSent(m);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  if (templates.isLoading) return <div className="muted">{t("common.loading")}</div>;
  if (!template) {
    return (
      <div className="muted">
        {t("send.noTemplates")} <Link to="/templates">{t("nav.templates")}</Link>
      </div>
    );
  }
  return (
    <form className="form" onSubmit={submit}>
      <label className="field">
        {t("send.template")}
        <select
          value={template.id}
          onChange={(e) => {
            setTemplateId(e.target.value);
            setParams([]);
          }}
        >
          {usable.map((tp) => (
            <option key={tp.id} value={tp.id}>{`${tp.name} (${tp.language})`}</option>
          ))}
        </select>
      </label>
      <div className="muted small">{template.components.find((c) => c.type.toUpperCase() === "BODY")?.text}</div>
      {vars.map((v, i) => (
        <label key={v} className="field">
          {t("templates.bodyVar", { v })}
          <input value={params[i] ?? ""} onChange={(e) => setParams(vars.map((_, j) => (j === i ? e.target.value : params[j] ?? "")))} required />
        </label>
      ))}
      {error && <div className="error">{error}</div>}
      <div className="actions">
        <button className="primary" disabled={busy}>{busy ? t("send.sending") : t("send.sendTemplate")}</button>
      </div>
    </form>
  );
}
