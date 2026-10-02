import { useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { usePhoneNumbers } from "../api/hooks";
import type { Template, TemplateCategory } from "../api/types";
import { buildComponents, placeholders, toTemplateName } from "../lib/templates";

// Meta language codes offered first; any other code can be typed.
const LANGUAGES = ["en", "en_US", "hi", "ta", "te", "mr", "bn", "kn", "ml", "gu", "pa", "ur"];

export default function NewTemplate() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const numbers = usePhoneNumbers();
  const accounts = useMemo(() => {
    const seen = new Map<string, string>();
    for (const n of numbers.data ?? []) {
      if (n.status === "connected" && !seen.has(n.whatsapp_account_id)) {
        seen.set(n.whatsapp_account_id, `${n.verified_name ?? n.display_phone_number} (${n.waba_id})`);
      }
    }
    return [...seen.entries()];
  }, [numbers.data]);

  const [account, setAccount] = useState("");
  const [name, setName] = useState("");
  const [category, setCategory] = useState<TemplateCategory>("utility");
  const [language, setLanguage] = useState("en");
  const [header, setHeader] = useState("");
  const [body, setBody] = useState("");
  const [footer, setFooter] = useState("");
  const [replies, setReplies] = useState(["", "", ""]);
  const [samples, setSamples] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const accountId = account || accounts[0]?.[0] || "";
  const vars = [
    ...placeholders(header).map((v) => ({ key: `h:${v}`, label: t("templates.headerVar", { v }) })),
    ...placeholders(body).map((v) => ({ key: `b:${v}`, label: t("templates.bodyVar", { v }) })),
  ];

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api<Template>("POST", "/v1/templates", {
        whatsapp_account_id: accountId,
        name,
        language,
        category,
        components: buildComponents({ header, body, footer, quickReplies: replies, samples }),
      });
      await qc.invalidateQueries({ queryKey: ["templates"] });
      navigate("/templates");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  if (numbers.data && accounts.length === 0) {
    return (
      <section>
        <h1>{t("templates.newTitle")}</h1>
        <div className="card muted">
          {t("templates.needNumber")} <Link to="/numbers/connect">{t("numbers.connect")}</Link>
        </div>
      </section>
    );
  }

  return (
    <section className="narrow">
      <h1>{t("templates.newTitle")}</h1>
      <p className="muted">{t("templates.newIntro")}</p>
      <form className="card form" onSubmit={submit}>
        {accounts.length > 1 && (
          <label className="field">
            {t("templates.account")}
            <select value={accountId} onChange={(e) => setAccount(e.target.value)}>
              {accounts.map(([id, label]) => (
                <option key={id} value={id}>{label}</option>
              ))}
            </select>
          </label>
        )}
        <label className="field">
          {t("templates.name")}
          <input value={name} onChange={(e) => setName(toTemplateName(e.target.value))} required placeholder="order_shipped" />
          <span className="muted small">{t("templates.nameHint")}</span>
        </label>
        <div className="row">
          <label className="field">
            {t("templates.category")}
            <select value={category} onChange={(e) => setCategory(e.target.value as TemplateCategory)}>
              {(["utility", "marketing", "authentication"] as const).map((c) => (
                <option key={c} value={c}>{t(`templates.category_${c}`)}</option>
              ))}
            </select>
          </label>
          <label className="field">
            {t("templates.language")}
            <input list="template-languages" value={language} onChange={(e) => setLanguage(e.target.value.trim())} required />
            <datalist id="template-languages">
              {LANGUAGES.map((l) => <option key={l} value={l} />)}
            </datalist>
          </label>
        </div>
        <label className="field">
          {t("templates.header")}
          <input value={header} onChange={(e) => setHeader(e.target.value)} maxLength={60} />
        </label>
        <label className="field">
          {t("templates.body")}
          <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={5} maxLength={1024} required />
          <span className="muted small">{t("templates.bodyHint")}</span>
        </label>
        {vars.length > 0 && (
          <fieldset className="field">
            <legend>{t("templates.samples")}</legend>
            {vars.map((v) => (
              <label key={v.key} className="field">
                {v.label}
                <input value={samples[v.key] ?? ""} onChange={(e) => setSamples({ ...samples, [v.key]: e.target.value })} required />
              </label>
            ))}
          </fieldset>
        )}
        <label className="field">
          {t("templates.footer")}
          <input value={footer} onChange={(e) => setFooter(e.target.value)} maxLength={60} />
        </label>
        <fieldset className="field">
          <legend>{t("templates.quickReplies")}</legend>
          {replies.map((r, i) => (
            <input
              key={i}
              value={r}
              maxLength={25}
              onChange={(e) => setReplies(replies.map((x, j) => (j === i ? e.target.value : x)))}
            />
          ))}
        </fieldset>
        {error && <div className="error">{error}</div>}
        <div className="actions">
          <Link className="button" to="/templates">{t("common.cancel")}</Link>
          <button className="primary" disabled={busy || !accountId}>{busy ? t("templates.submitting") : t("templates.submit")}</button>
        </div>
      </form>
    </section>
  );
}
