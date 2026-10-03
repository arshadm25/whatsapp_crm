import { useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers } from "../api/hooks";
import type { Template, TemplateCategory } from "../api/types";
import { buildComponents, placeholders, toTemplateName } from "../lib/templates";
import Icon from "../components/Icon";
import { initials } from "../lib/time";

// Meta language codes offered first; any other code can be typed.
const LANGUAGES = ["en", "en_US", "hi", "ta", "te", "mr", "bn", "kn", "ml", "gu", "pa", "ur"];
const MAX_BUTTONS = 3;

export default function NewTemplate() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useMe().data;
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
  const [replies, setReplies] = useState<string[]>([]);
  const [samples, setSamples] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const accountId = account || accounts[0]?.[0] || "";
  const vars = [
    ...placeholders(header).map((v) => ({ key: `h:${v}`, v, label: t("templates.headerVar", { v }) })),
    ...placeholders(body).map((v) => ({ key: `b:${v}`, v, label: t("templates.bodyVar", { v }) })),
  ];

  // Meta's checks, run live so the aside shows what still needs fixing.
  const checks = [
    { key: "name", ok: /^[a-z0-9_]+$/.test(name) },
    { key: "samples", ok: vars.every((v) => (samples[v.key] ?? "").trim() !== "") },
    { key: "footer", ok: footer.length <= 60 },
    { key: "buttons", ok: replies.length <= MAX_BUTTONS && replies.every((r) => r.trim().length > 0 && r.length <= 25) },
  ];
  const passed = checks.filter((c) => c.ok).length;

  const addVariable = () => {
    const next = placeholders(body).length + 1;
    setBody((b) => `${b}${b && !b.endsWith(" ") ? " " : ""}{{${next}}}`);
  };

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

  const fill = (text: string, prefix: "h" | "b") =>
    text.split(/(\{\{\s*[A-Za-z0-9_]+\s*\}\})/g).map((part, i) => {
      const m = part.match(/^\{\{\s*([A-Za-z0-9_]+)\s*\}\}$/);
      if (!m) return part;
      return <b key={i}>{samples[`${prefix}:${m[1]}`] || part}</b>;
    });

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
    <section>
      <div className="page-head">
        <div>
          <div className="crumb"><Link to="/templates">{t("templates.title")}</Link><Icon name="chevronRight" />{t("templates.newTitle")}</div>
          <h1>{t("templates.newTitle")}</h1>
          <p className="sub">{t("templates.newIntro")}</p>
        </div>
        <div className="actions">
          <Link className="button" to="/templates">{t("common.cancel")}</Link>
          <button className="primary" form="new-template" disabled={busy || !accountId || !body.trim()}>
            <Icon name="send" size="s" />{busy ? t("templates.submitting") : t("templates.submit")}
          </button>
        </div>
      </div>
      <div className="split">
        <form id="new-template" className="card flush tpl" onSubmit={submit}>
          <div className="sec">
            <h3><span className="stepn">1</span>{t("templates.basics")}</h3>
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
            <div className="grid g2">
              <label className="field">
                {t("templates.name")}
                <input value={name} onChange={(e) => setName(toTemplateName(e.target.value))} required placeholder="order_shipped" />
                <span className="hint">{t("templates.nameHint")}</span>
              </label>
              <label className="field">
                {t("templates.language")}
                <input list="template-languages" value={language} onChange={(e) => setLanguage(e.target.value.trim())} required />
                <datalist id="template-languages">
                  {LANGUAGES.map((l) => <option key={l} value={l} />)}
                </datalist>
                <span className="hint">{t("templates.languageHint")}</span>
              </label>
            </div>
            <div className="field">
              {t("templates.category")}
              <div className="segmented" role="radiogroup" aria-label={t("templates.category")}>
                {(["marketing", "utility", "authentication"] as const).map((c) => (
                  <button key={c} type="button" className={category === c ? "on" : ""} onClick={() => setCategory(c)}>{t(`templates.category_${c}`)}</button>
                ))}
              </div>
              <span className="hint">{t(`templates.categoryHint_${category}`)}</span>
            </div>
          </div>

          <div className="sec">
            <h3><span className="stepn">2</span>{t("templates.content")}</h3>
            <label className="field">
              <span>{t("templates.headerLabel")} <span className="hint">{t("templates.optional")}</span></span>
              <input value={header} onChange={(e) => setHeader(e.target.value)} maxLength={60} />
            </label>
            <label className="field">
              {t("templates.body")}
              <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={5} maxLength={1024} required />
            </label>
            <div className="tpl-tools">
              <button type="button" className="sm ghost" onClick={addVariable}><Icon name="plus" size="xs" />{t("templates.addVariable")}</button>
              <span className="sp" />
              <span className="muted small">{body.length} / 1024</span>
            </div>
            {vars.length > 0 && (
              <div className="samples">
                <span className="sl">{t("templates.samples")} <span className="hint">{t("templates.samplesHint")}</span></span>
                {vars.map((v) => (
                  <label key={v.key} className="sample">
                    <span className="var">{`{{${v.v}}}`}</span>
                    <span className="field">
                      <input value={samples[v.key] ?? ""} onChange={(e) => setSamples({ ...samples, [v.key]: e.target.value })} required aria-label={v.label} placeholder={v.label} />
                    </span>
                  </label>
                ))}
              </div>
            )}
            <label className="field">
              {t("templates.footerLabel")}
              <input value={footer} onChange={(e) => setFooter(e.target.value)} maxLength={60} />
              <span className="hint">{t("templates.footerHint")}</span>
            </label>
          </div>

          <div className="sec last">
            <h3><span className="stepn">3</span>{t("templates.buttons")} <span className="hint">{t("templates.optional")}</span></h3>
            <div className="btn-list">
              {replies.map((r, i) => (
                <span key={i} className="btn-row">
                  <input
                    value={r}
                    maxLength={25}
                    placeholder={t("templates.buttonText")}
                    aria-label={t("templates.buttonText")}
                    onChange={(e) => setReplies(replies.map((x, j) => (j === i ? e.target.value : x)))}
                  />
                  <button type="button" className="ib gh sm" aria-label={t("templates.removeButton")} onClick={() => setReplies(replies.filter((_, j) => j !== i))}>
                    <Icon name="x" size="xs" />
                  </button>
                </span>
              ))}
              {replies.length < MAX_BUTTONS && (
                <button type="button" className="lnk" onClick={() => setReplies([...replies, ""])}><Icon name="plus" size="xs" />{t("templates.addButton")}</button>
              )}
            </div>
            <span className="hint">{t("templates.quickReplies")}</span>
            {error && <div className="error" style={{ margin: 0 }}>{error}</div>}
          </div>
        </form>

        <aside className="stack">
          <div className="card flush">
            <div className="chd"><h2>{t("templates.preview")}</h2><span className="pill">{t("templates.status_draft")}</span></div>
            <div className="cb phone-wrap">
              <div className="phone">
                <div className="phone-head">
                  <span className="av">{initials(me?.tenant?.name ?? "")}</span>
                  <span><b>{me?.tenant?.name}</b><span>{t("templates.businessAccount")}</span></span>
                </div>
                <div className="phone-body">
                  <div className="pbubble">
                    {header && <span className="ph">{fill(header, "h")}</span>}
                    {body ? fill(body, "b") : <span className="muted">{t("templates.previewEmpty")}</span>}
                    {footer && <span className="pf">{footer}</span>}
                    <span className="pt">{new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span>
                  </div>
                  {replies.filter((r) => r.trim()).map((r, i) => <div key={i} className="pbtn">{r}</div>)}
                </div>
              </div>
            </div>
          </div>
          <div className="card flush">
            <div className="chd">
              <div><h2>{t("templates.checks")}</h2><p>{t("templates.checksSub")}</p></div>
              <span className={`pill ${passed === checks.length ? "ok" : "wa"}`}>{t("templates.checksCount", { passed, total: checks.length })}</span>
            </div>
            <ul className="lst cb">
              {checks.map((c) => (
                <li key={c.key} className={c.ok ? "ok" : "no"}><Icon name={c.ok ? "check" : "x"} size="s" />{t(`templates.check_${c.key}`)}</li>
              ))}
            </ul>
            <div className="cf"><span><Icon name="clock" size="xs" /> {t("templates.checksFoot")}</span></div>
          </div>
        </aside>
      </div>
    </section>
  );
}
