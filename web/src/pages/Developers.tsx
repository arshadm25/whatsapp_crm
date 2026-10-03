import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers } from "../api/hooks";
import type { APIKey, CreatedAPIKey } from "../api/types";
import Webhooks from "../components/Webhooks";
import Icon from "../components/Icon";
import { ago, shortDate } from "../lib/time";

type Snippet = "curl" | "node";

export default function Developers() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const allowed = role === "owner" || role === "admin" || role === "developer";
  const qc = useQueryClient();
  const numbers = usePhoneNumbers();
  const keys = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => (await api<{ data: APIKey[] }>("GET", "/internal/developers/api-keys")).data,
    enabled: allowed,
  });
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [mode, setMode] = useState<"live" | "sandbox">("live");
  const [created, setCreated] = useState<CreatedAPIKey | null>(null);
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [snippet, setSnippet] = useState<Snippet>("curl");

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const k = await api<CreatedAPIKey>("POST", "/internal/developers/api-keys", { name, phone_number_id: phone || null, mode });
      setCreated(k);
      setCopied(false);
      setName("");
      setPhone("");
      setCreating(false);
      await qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (k: APIKey) => {
    if (!window.confirm(t("developers.confirmRevoke", { name: k.name }))) return;
    setError("");
    try {
      await api("DELETE", `/internal/developers/api-keys/${k.id}`);
      if (created?.id === k.id) setCreated(null);
      await qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  const copy = async () => {
    if (!created) return;
    await navigator.clipboard?.writeText(created.key);
    setCopied(true);
  };

  const numberLabel = (id: string | null) =>
    id ? numbers.data?.find((n) => n.id === id)?.display_phone_number ?? id : t("developers.allNumbers");

  if (!allowed) return <section><h1>{t("developers.title")}</h1><div className="card muted">{t("developers.noAccess")}</div></section>;

  const origin = window.location.origin;
  const phoneId = numbers.data?.[0]?.id ?? "PHONE_NUMBER_ID";
  const code = snippet === "curl" ? (
    <>
      <span className="c1">curl</span> {origin}/v1/messages \{"\n"}
      {"  "}-H <span className="c3">"Authorization: Bearer $ECOGO_KEY"</span> \{"\n"}
      {"  "}-H <span className="c3">"Content-Type: application/json"</span> \{"\n"}
      {"  "}-d <span className="c3">{`'{
    "phone_number_id": "${phoneId}",
    "to": "919876500011",
    "type": "template",
    "template": { "name": "hello_world", "language": "en_US" }
  }'`}</span>
    </>
  ) : (
    <>
      <span className="c1">await</span> fetch(<span className="c3">"{origin}/v1/messages"</span>, {"{"}{"\n"}
      {"  "}method: <span className="c3">"POST"</span>,{"\n"}
      {"  "}headers: {"{"} Authorization: <span className="c3">`Bearer ${"{"}process.env.ECOGO_KEY{"}"}`</span>, <span className="c3">"Content-Type"</span>: <span className="c3">"application/json"</span> {"}"},{"\n"}
      {"  "}body: JSON.stringify({"{"}{"\n"}
      {"    "}phone_number_id: <span className="c3">"{phoneId}"</span>,{"\n"}
      {"    "}to: <span className="c3">"919876500011"</span>,{"\n"}
      {"    "}type: <span className="c3">"template"</span>,{"\n"}
      {"    "}template: {"{"} name: <span className="c3">"hello_world"</span>, language: <span className="c3">"en_US"</span> {"}"},{"\n"}
      {"  "}{"}"}),{"\n"}
      {"}"});
    </>
  );

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("developers.title")}</h1>
          <p className="sub">{t("developers.intro")}</p>
        </div>
        <div className="actions">
          <a className="button" href="/v1/docs" target="_blank" rel="noreferrer"><Icon name="book" size="s" />{t("developers.apiDocs")}</a>
          <button className="primary" onClick={() => setCreating(!creating)}><Icon name="plus" size="s" />{t("developers.createKey")}</button>
        </div>
      </div>
      <div className="split">
        <div className="stack">
          {created && (
            <div className="card new-key">
              <strong>{t("developers.copyNow")}</strong>
              <div className="key-row">
                <code>{created.key}</code>
                <button className="sm" onClick={copy}><Icon name="copy" size="s" />{copied ? t("developers.copied") : t("developers.copy")}</button>
              </div>
              <div className="muted small">{t("developers.usage")}</div>
            </div>
          )}
          {error && <div className="error" style={{ margin: 0 }}>{error}</div>}
          <div className="card flush">
            <div className="chd"><div><h2>{t("developers.keys")}</h2><p>{t("developers.keysSub")}</p></div></div>
            {creating && (
              <form className="inline-form in-card" onSubmit={create}>
                <label className="field">
                  {t("developers.keyName")}
                  <input value={name} onChange={(e) => setName(e.target.value)} maxLength={80} required placeholder={t("developers.keyNameHint")} autoFocus />
                </label>
                <label className="field">
                  {t("developers.number")}
                  <select value={phone} onChange={(e) => setPhone(e.target.value)}>
                    <option value="">{t("developers.allNumbers")}</option>
                    {numbers.data?.map((n) => (
                      <option key={n.id} value={n.id}>{n.display_phone_number}</option>
                    ))}
                  </select>
                </label>
                <label className="field">
                  {t("developers.mode")}
                  <select value={mode} onChange={(e) => setMode(e.target.value as "live" | "sandbox")}>
                    <option value="live">{t("developers.modeLive")}</option>
                    <option value="sandbox">{t("developers.modeSandbox")}</option>
                  </select>
                </label>
                <div className="actions">
                  <button type="button" onClick={() => setCreating(false)}>{t("common.cancel")}</button>
                  <button className="primary" disabled={busy || !name.trim()}>{t("developers.create")}</button>
                </div>
              </form>
            )}
            {keys.data?.length === 0 && !creating && <div className="cb muted">{t("developers.noKeys")}</div>}
            {!!keys.data?.length && (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>{t("developers.keyName")}</th>
                      <th>{t("developers.key")}</th>
                      <th>{t("developers.scope")}</th>
                      <th>{t("developers.lastUsed")}</th>
                      <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {keys.data.map((k) => (
                      <tr key={k.id} className={k.revoked_at ? "muted" : ""}>
                        <td>
                          <div className="who">
                            <span className={k.mode === "sandbox" || k.revoked_at ? "ic gy" : "ic"}><Icon name="key" size="s" /></span>
                            <span><b>{k.name}</b><small>{t("developers.createdOn", { date: shortDate(k.created_at) })}</small></span>
                          </div>
                        </td>
                        <td className="k">{k.prefix}••••</td>
                        <td><span className="chip gy">{k.mode === "sandbox" ? t("developers.sandbox") : numberLabel(k.phone_number_id)}</span></td>
                        <td>{k.last_used_at ? ago(k.last_used_at, t) : t("developers.never")}</td>
                        <td className="r">
                          {k.revoked_at ? (
                            <span className="pill">{t("developers.revoked")}</span>
                          ) : (
                            <button className="sm bdg" onClick={() => revoke(k)}>{t("developers.revoke")}</button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
          <Webhooks />
        </div>
        <aside className="stack">
          <div className="card flush">
            <div className="chd">
              <h2>{t("developers.quickstart")}</h2>
              <div className="segmented">
                {(["curl", "node"] as Snippet[]).map((s) => (
                  <button key={s} type="button" className={snippet === s ? "on" : ""} onClick={() => setSnippet(s)}>{s === "curl" ? "cURL" : "Node"}</button>
                ))}
              </div>
            </div>
            <div className="cb"><pre className="code">{code}</pre></div>
          </div>
          <div className="card flush">
            <div className="chd"><h2>{t("developers.resources")}</h2></div>
            <a className="res" href="/v1/docs" target="_blank" rel="noreferrer">
              <Icon name="book" size="s" /><span className="t">{t("developers.docsLink")}</span><Icon name="external" size="s" />
            </a>
            <a className="res" href="/v1/docs#webhooks" target="_blank" rel="noreferrer">
              <Icon name="shield" size="s" /><span className="t">{t("developers.signaturesLink")}</span><Icon name="external" size="s" />
            </a>
            <a className="res" href="/v1/openapi.yaml" target="_blank" rel="noreferrer">
              <Icon name="code" size="s" /><span className="t">{t("developers.openapiLink")}</span><Icon name="external" size="s" />
            </a>
          </div>
        </aside>
      </div>
    </section>
  );
}
