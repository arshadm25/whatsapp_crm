import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../api/client";
import { useMe, usePhoneNumbers } from "../../api/hooks";
import type { APIKey, CreatedAPIKey, KeyUsageReport } from "../../api/types";
import Icon from "../../components/Icon";
import { canUseDevelopers, DevTabs, RangePicker, ShareRows, StackedBars, Stat, type DevRange } from "../../components/DevCharts";
import { buckets, fill, pct } from "../../lib/devAnalytics";
import { ago, shortDate } from "../../lib/time";

type Snippet = "curl" | "node";

// Developers > API keys: usage over time and per key, the keys themselves, and a quick start.
export default function ApiKeys() {
  const { t } = useTranslation();
  const allowed = canUseDevelopers(useMe().data?.tenant?.role);
  const qc = useQueryClient();
  const numbers = usePhoneNumbers();
  const [days, setDays] = useState<DevRange>(7);
  const keys = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => (await api<{ data: APIKey[] }>("GET", "/internal/developers/api-keys")).data,
    enabled: allowed,
  });
  const usage = useQuery({
    queryKey: ["developer-usage", days],
    queryFn: () => api<KeyUsageReport>("GET", `/internal/developers/usage?days=${days}`),
    enabled: allowed,
    refetchInterval: 60_000,
  });
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [mode, setMode] = useState<"live" | "sandbox">("live");
  const [created, setCreated] = useState<CreatedAPIKey | null>(null);
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

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

  if (!allowed) return <section><h1>{t("nav.apiKeys")}</h1><div className="card muted">{t("developers.noAccess")}</div></section>;

  const u = usage.data;
  const usageFor = (id: string) => u?.by_key.find((k) => k.api_key_id === id) ?? { calls: 0, errors: 0 };
  const active = keys.data?.filter((k) => !k.revoked_at) ?? [];
  const lastUsed = active.map((k) => k.last_used_at).filter((x): x is string => !!x).sort().at(-1);
  const keyName = (id: string) => keys.data?.find((k) => k.id === id)?.name ?? id.slice(0, 8);
  const list = u ? buckets(u) : [];
  const rangeLabel = days === 1 ? t("devpages.last24h") : t("analytics.days", { count: days });

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("nav.apiKeys")}</h1>
          <p className="sub">{t("devpages.keysIntro")}</p>
        </div>
        <div className="actions">
          <RangePicker value={days} onChange={setDays} />
          <a className="button" href="/v1/docs" target="_blank" rel="noreferrer"><Icon name="book" size="s" />{t("developers.apiDocs")}</a>
          <button className="primary" onClick={() => setCreating(!creating)}><Icon name="plus" size="s" />{t("developers.createKey")}</button>
        </div>
      </div>
      <DevTabs />
      <div className="stack">
        <div className="grid g4">
          <Stat label={t("devpages.apiCalls")} value={u ? u.calls.toLocaleString() : "—"} icon="activity" foot={rangeLabel} />
          <Stat label={t("devpages.errorRate")} value={u ? pct(u.errors, u.calls) : "—"} icon="alert" tone={u && u.errors > 0 ? "rd" : "gy"}
            foot={t("devpages.errorsCount", { count: u?.errors ?? 0 })} />
          <Stat label={t("devpages.activeKeys")} value={String(active.length)} icon="key" tone="bl"
            foot={t("devpages.sandboxKeys", { count: active.filter((k) => k.mode === "sandbox").length })} />
          <Stat label={t("devpages.lastCall")} value={lastUsed ? ago(lastUsed, t) : t("developers.never")} icon="clock" tone="gy"
            foot={t("devpages.lastCallFoot")} />
        </div>

        <div className="split">
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.callsOverTime")}</h2><p>{u?.hourly ? t("devpages.byHour") : t("devpages.byDay")}</p></div></div>
            <div className="cb">
              {u ? (
                <StackedBars buckets={list} label={t("devpages.callsOverTime")} series={[
                  { key: "ok", label: t("devpages.successful"), color: "var(--accent)", values: fill(u, list, u.points, (p) => p.calls - p.errors) },
                  { key: "err", label: t("devpages.errors"), color: "var(--danger)", values: fill(u, list, u.points, (p) => p.errors) },
                ]} />
              ) : <div className="muted">{t("common.loading")}</div>}
            </div>
          </div>
          <div className="card flush">
            <div className="chd"><div><h2>{t("devpages.callsByKey")}</h2><p>{rangeLabel}</p></div></div>
            <div className="cb cats-list">
              <ShareRows total={u?.calls ?? 0} rows={(u?.by_key ?? []).map((k) => ({ key: k.api_key_id, label: keyName(k.api_key_id), n: k.calls }))} />
            </div>
          </div>
        </div>

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
                        <th className="r">{t("devpages.calls")}</th>
                        <th className="r">{t("devpages.errors")}</th>
                        <th>{t("developers.lastUsed")}</th>
                        <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
                      </tr>
                    </thead>
                    <tbody>
                      {keys.data.map((k) => {
                        const ku = usageFor(k.id);
                        return (
                          <tr key={k.id} className={k.revoked_at ? "muted" : ""}>
                            <td>
                              <div className="who">
                                <span className={k.mode === "sandbox" || k.revoked_at ? "ic gy" : "ic"}><Icon name="key" size="s" /></span>
                                <span><b>{k.name}</b><small>{t("developers.createdOn", { date: shortDate(k.created_at) })}</small></span>
                              </div>
                            </td>
                            <td className="k">{k.prefix}••••</td>
                            <td><span className="chip gy">{k.mode === "sandbox" ? t("developers.sandbox") : numberLabel(k.phone_number_id)}</span></td>
                            <td className="r num-t">{ku.calls.toLocaleString()}</td>
                            <td className="r num-t">{ku.errors ? <span title={pct(ku.errors, ku.calls)} style={{ color: "var(--danger)" }}>{ku.errors.toLocaleString()}</span> : 0}</td>
                            <td>{k.last_used_at ? ago(k.last_used_at, t) : t("developers.never")}</td>
                            <td className="r">
                              {k.revoked_at ? (
                                <span className="pill">{t("developers.revoked")}</span>
                              ) : (
                                <button className="sm bdg" onClick={() => revoke(k)}>{t("developers.revoke")}</button>
                              )}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="cf muted small">{t("devpages.keysTableFoot", { range: rangeLabel })}</div>
            </div>
        </div>
        <div className="split">
          <Quickstart phoneId={numbers.data?.[0]?.id ?? "PHONE_NUMBER_ID"} />
          <Resources />
        </div>
      </div>
    </section>
  );
}

function Quickstart({ phoneId }: { phoneId: string }) {
  const { t } = useTranslation();
  const [snippet, setSnippet] = useState<Snippet>("curl");
  const origin = window.location.origin;
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
  );
}

export function Resources() {
  const { t } = useTranslation();
  return (
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
  );
}
