import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { usePhoneNumbers, useTemplates } from "../api/hooks";
import type { Message } from "../api/types";
import { bodyVariables } from "../lib/templates";

const FINAL = ["read", "failed"];

// A simple sender for testing a connected number until the inbox arrives.
export default function SendMessage() {
  const { t } = useTranslation();
  const numbers = usePhoneNumbers();
  const connected = (numbers.data ?? []).filter((n) => n.status === "connected");
  const templates = useTemplates("approved");

  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [mode, setMode] = useState<"template" | "text">("template");
  const [text, setText] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [params, setParams] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [sentId, setSentId] = useState("");

  const fromId = from || connected[0]?.id || "";
  const account = connected.find((n) => n.id === fromId)?.whatsapp_account_id;
  const usable = (templates.data ?? []).filter((tp) => tp.whatsapp_account_id === account);
  const template = usable.find((tp) => tp.id === templateId) ?? usable[0];
  const vars = template ? bodyVariables(template.components) : [];

  const sent = useQuery({
    queryKey: ["message", sentId],
    queryFn: () => api<Message>("GET", `/v1/messages/${sentId}`),
    enabled: !!sentId,
    refetchInterval: (q) => (q.state.data && FINAL.includes(q.state.data.status) ? false : 2000),
  });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    setSentId("");
    const digits = to.replace(/\D/g, "");
    const body =
      mode === "text"
        ? { phone_number_id: fromId, to: digits, type: "text", text: { body: text } }
        : {
            phone_number_id: fromId,
            to: digits,
            type: "template",
            template: {
              name: template!.name,
              language: template!.language,
              ...(vars.length && {
                components: [{ type: "body", parameters: vars.map((_, i) => ({ type: "text", text: params[i] ?? "" })) }],
              }),
            },
          };
    try {
      const m = await api<Message>("POST", "/v1/messages", body);
      setSentId(m.id);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  if (numbers.data && connected.length === 0) {
    return (
      <section>
        <h1>{t("send.title")}</h1>
        <div className="card muted">
          {t("templates.needNumber")} <Link to="/numbers/connect">{t("numbers.connect")}</Link>
        </div>
      </section>
    );
  }

  return (
    <section className="narrow">
      <h1>{t("send.title")}</h1>
      <p className="muted">{t("send.intro")}</p>
      <form className="card form" onSubmit={submit}>
        {connected.length > 1 && (
          <label className="field">
            {t("send.from")}
            <select value={fromId} onChange={(e) => setFrom(e.target.value)}>
              {connected.map((n) => (
                <option key={n.id} value={n.id}>{n.display_phone_number}</option>
              ))}
            </select>
          </label>
        )}
        <label className="field">
          {t("send.to")}
          <input value={to} onChange={(e) => setTo(e.target.value)} placeholder="919876543210" inputMode="tel" required />
          <span className="muted small">{t("send.toHint")}</span>
        </label>
        <div className="choices">
          {(["template", "text"] as const).map((m) => (
            <label key={m} className={`choice ${mode === m ? "selected" : ""}`}>
              <input type="radio" checked={mode === m} onChange={() => setMode(m)} />
              <div>
                <strong>{t(`send.mode_${m}`)}</strong>
                <div className="muted small">{t(`send.mode_${m}_hint`)}</div>
              </div>
            </label>
          ))}
        </div>
        {mode === "text" ? (
          <label className="field">
            {t("send.message")}
            <textarea value={text} onChange={(e) => setText(e.target.value)} rows={4} maxLength={4096} required />
          </label>
        ) : usable.length === 0 ? (
          <div className="muted">
            {t("send.noTemplates")} <Link to="/templates">{t("nav.templates")}</Link>
          </div>
        ) : (
          <>
            <label className="field">
              {t("send.template")}
              <select
                value={template?.id}
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
            <div className="muted small">{template?.components.find((c) => c.type.toUpperCase() === "BODY")?.text}</div>
            {vars.map((v, i) => (
              <label key={v} className="field">
                {t("templates.bodyVar", { v })}
                <input value={params[i] ?? ""} onChange={(e) => setParams(vars.map((_, j) => (j === i ? e.target.value : params[j] ?? "")))} required />
              </label>
            ))}
          </>
        )}
        {error && <div className="error">{error}</div>}
        <div className="actions">
          <button className="primary" disabled={busy || (mode === "template" && !template)}>
            {busy ? t("send.sending") : t("send.send")}
          </button>
        </div>
      </form>
      {sent.data && (
        <div className="card">
          <strong>{t("send.statusTitle")}</strong>{" "}
          <span className={`pill m-${sent.data.status}`}>{t(`send.status_${sent.data.status}`)}</span>
          {sent.data.error && <div className="error">{sent.data.error.message}</div>}
        </div>
      )}
    </section>
  );
}
