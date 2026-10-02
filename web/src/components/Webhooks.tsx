import { Fragment, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { usePhoneNumbers } from "../api/hooks";
import type { CreatedWebhookEndpoint, Page, WebhookDelivery, WebhookEndpoint, WebhookEventType } from "../api/types";
import Icon from "./Icon";
import { shortTime } from "../lib/time";

const EVENTS: WebhookEventType[] = ["message.received", "message.status", "template.status", "number.quality", "bot.handoff", "flow.submission"];

// The Developers screen's webhook section: endpoints with their events, then the delivery log
// of the endpoint picked (the first one by default).
export default function Webhooks() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const numbers = usePhoneNumbers();
  const endpoints = useQuery({
    queryKey: ["webhook-endpoints"],
    queryFn: async () => (await api<{ data: WebhookEndpoint[] }>("GET", "/v1/webhook-endpoints")).data,
  });
  const [adding, setAdding] = useState(false);
  const [url, setUrl] = useState("https://");
  const [description, setDescription] = useState("");
  const [events, setEvents] = useState<WebhookEventType[]>(EVENTS);
  const [phone, setPhone] = useState("");
  const [created, setCreated] = useState<CreatedWebhookEndpoint | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  const [shown, setShown] = useState<{ id: string; secret: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const current = endpoints.data?.find((ep) => ep.id === picked) ?? endpoints.data?.[0];
  const toggle = (e: WebhookEventType) => setEvents(events.includes(e) ? events.filter((x) => x !== e) : [...events, e]);

  const create = async (ev: FormEvent) => {
    ev.preventDefault();
    setBusy(true);
    setError("");
    try {
      const ep = await api<CreatedWebhookEndpoint>("POST", "/v1/webhook-endpoints", {
        url: url.trim(),
        description,
        event_types: events,
        phone_number_id: phone || null,
      });
      setCreated(ep);
      setPicked(ep.id);
      setUrl("https://");
      setDescription("");
      setAdding(false);
      await qc.invalidateQueries({ queryKey: ["webhook-endpoints"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  // Shows the signing secret again, or replaces it with a new one.
  const secret = async (ep: WebhookEndpoint, rotate: boolean) => {
    if (rotate && !window.confirm(t("developers.confirmRotate"))) return;
    setError("");
    try {
      const r = await api<{ secret: string }>("POST", `/v1/webhook-endpoints/${ep.id}/secret${rotate ? "/rotate" : ""}`);
      setShown({ id: ep.id, secret: r.secret });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  const remove = async (ep: WebhookEndpoint) => {
    if (!window.confirm(t("developers.confirmDelete", { url: ep.url }))) return;
    try {
      await api("DELETE", `/v1/webhook-endpoints/${ep.id}`);
      if (created?.id === ep.id) setCreated(null);
      await qc.invalidateQueries({ queryKey: ["webhook-endpoints"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  const numberLabel = (id: string | null) =>
    id ? numbers.data?.find((n) => n.id === id)?.display_phone_number ?? id : t("developers.allNumbers");

  return (
    <>
      <div className="card flush">
        <div className="chd">
          <div><h2>{t("developers.webhooks")}</h2><p>{t("developers.webhooksSub")}</p></div>
          <button className="sm" onClick={() => setAdding(!adding)}><Icon name="plus" size="s" />{t("developers.addEndpoint")}</button>
        </div>
        {adding && (
          <form className="inline-form in-card" onSubmit={create}>
            <label className="field">
              {t("developers.url")}
              <input type="url" value={url} onChange={(e) => setUrl(e.target.value)} required pattern="https://.+" autoFocus />
            </label>
            <label className="field">
              {t("developers.description")}
              <input value={description} onChange={(e) => setDescription(e.target.value)} maxLength={200} />
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
            <fieldset className="event-picks">
              <legend>{t("developers.events")}</legend>
              {EVENTS.map((e) => (
                <label key={e} className="check">
                  <input type="checkbox" checked={events.includes(e)} onChange={() => toggle(e)} />
                  {t(`developers.event_${e}`)}
                </label>
              ))}
            </fieldset>
            <div className="actions">
              <button type="button" onClick={() => setAdding(false)}>{t("common.cancel")}</button>
              <button className="primary" disabled={busy || !events.length}>{t("developers.addEndpoint")}</button>
            </div>
          </form>
        )}
        {error && <div className="error">{error}</div>}
        {endpoints.data?.length === 0 && !adding && <div className="cb muted">{t("developers.noEndpoints")}</div>}
        {endpoints.data?.map((ep) => (
          <div key={ep.id} className={`endpoint ${current?.id === ep.id && endpoints.data!.length > 1 ? "on" : ""}`}>
            <div className="endpoint-row">
              <span className={ep.enabled ? "ic" : "ic gy"}><Icon name="zap" size="s" /></span>
              <span className="k url">{ep.url}</span>
              <span className={`pill ${ep.enabled ? "ok" : ""}`}>{ep.enabled ? t("developers.active") : t("developers.disabled")}</span>
              <span className="muted small">{numberLabel(ep.phone_number_id)}</span>
              {endpoints.data!.length > 1 && (
                <button className="link" onClick={() => setPicked(ep.id)}>{t("developers.showLog")}</button>
              )}
              <button className="link" onClick={() => (shown?.id === ep.id ? setShown(null) : secret(ep, false))}>
                {shown?.id === ep.id ? t("developers.hideSecret") : t("developers.showSecret")}
              </button>
              <button className="link" onClick={() => secret(ep, true)}>{t("developers.rotateSecret")}</button>
              <button className="link danger" onClick={() => remove(ep)}>{t("developers.delete")}</button>
            </div>
            {ep.description && <div className="muted small">{ep.description}</div>}
            <div className="chips">
              {ep.event_types.map((e) => <span key={e} className="chip k">{e}</span>)}
            </div>
            {shown?.id === ep.id && (
              <div className="secret-row">
                <span className="muted small">{t("developers.signingSecret")}</span>
                <span className="k">{shown.secret}</span>
                <button className="ib gh sm" aria-label={t("developers.copy")} title={t("developers.copy")} onClick={() => navigator.clipboard?.writeText(shown.secret)}>
                  <Icon name="copy" size="s" />
                </button>
              </div>
            )}
            {created?.id === ep.id && (
              <div className="secret-row">
                <span className="muted small">{t("developers.signingSecret")}</span>
                <span className="k">{created.secret}</span>
                <button className="ib gh sm" aria-label={t("developers.copy")} title={t("developers.copy")} onClick={() => navigator.clipboard?.writeText(created.secret)}>
                  <Icon name="copy" size="s" />
                </button>
              </div>
            )}
          </div>
        ))}
        {created && <div className="cf">{t("developers.secretNow")}</div>}
      </div>
      {current && <Deliveries endpoint={current} />}
    </>
  );
}

function Deliveries({ endpoint }: { endpoint: WebhookEndpoint }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [status, setStatus] = useState<"" | "dead" | "retrying">("");
  const [payloadOf, setPayloadOf] = useState<string | null>(null);
  const payload = useQuery({
    queryKey: ["webhook-delivery", endpoint.id, payloadOf],
    queryFn: () => api<{ payload: unknown }>("GET", `/v1/webhook-endpoints/${endpoint.id}/deliveries/${payloadOf}`),
    enabled: !!payloadOf,
  });
  const key = ["webhook-deliveries", endpoint.id, status];
  const q = useQuery({
    queryKey: key,
    queryFn: () =>
      api<Page<WebhookDelivery>>("GET", `/v1/webhook-endpoints/${endpoint.id}/deliveries?limit=50${status ? `&status=${status}` : ""}`),
    refetchInterval: 10_000,
  });
  const retry = async (d: WebhookDelivery) => {
    await api("POST", `/v1/webhook-endpoints/${endpoint.id}/deliveries/${d.id}/retry`);
    await qc.invalidateQueries({ queryKey: key });
  };
  const dayAgo = new Date(Date.now() - 24 * 3600_000).toISOString();
  const recent = (q.data?.data ?? []).filter((d) => d.created_at >= dayAgo && d.status !== "pending");
  const ok = recent.filter((d) => d.status === "succeeded").length;
  const minutesUntil = (iso: string) => Math.max(1, Math.round((new Date(iso).getTime() - Date.now()) / 60000));

  const response = (d: WebhookDelivery) => {
    if (d.status === "pending") return <span className="pill">{t("developers.delivery_pending")}</span>;
    if (d.status === "succeeded") return <span className="pill ok">{d.last_response_code ?? 200} OK</span>;
    const label = d.last_response_code ? `${d.last_response_code} ${t("developers.errorWord")}` : d.last_error ?? t("developers.delivery_dead");
    return <span className={`pill ${d.status === "dead" ? "er" : "wa"}`} title={d.last_error ?? undefined}>{label}</span>;
  };

  return (
    <div className="card flush">
      <div className="chd">
        <div>
          <h2>{t("developers.deliveryLog")}</h2>
          <p>
            {endpoint.url}
            {!status && recent.length > 0 && <> · {t("developers.successRate", { pct: Math.round((ok * 100) / recent.length), count: recent.length })}</>}
          </p>
        </div>
        <div className="segmented">
          {(["", "dead", "retrying"] as const).map((f) => (
            <button key={f} type="button" className={status === f ? "on" : ""} onClick={() => setStatus(f)}>
              {t(`developers.filter_${f || "all"}`)}
            </button>
          ))}
        </div>
      </div>
      {!q.data && <div className="cb muted">{t("common.loading")}</div>}
      {q.data && !q.data.data.length && <div className="cb muted">{t("developers.noDeliveries")}</div>}
      {!!q.data?.data.length && (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("developers.event")}</th>
                <th>{t("developers.response")}</th>
                <th>{t("developers.attempts")}</th>
                <th>{t("developers.time")}</th>
                <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {q.data.data.map((d) => (
                <Fragment key={d.id}>
                <tr>
                  <td className="k">{d.event_type}</td>
                  <td>{response(d)}</td>
                  <td>
                    {d.attempt_count}
                    {d.status === "retrying" && d.next_attempt_at && <> · {t("developers.nextIn", { count: minutesUntil(d.next_attempt_at) })}</>}
                  </td>
                  <td title={new Date(d.created_at).toLocaleString()}>
                    {d.created_at >= dayAgo ? shortTime(d.created_at) : new Date(d.created_at).toLocaleString()}
                  </td>
                  <td className="r">
                    <span className="actions" style={{ justifyContent: "flex-end" }}>
                      <button className="lnk" onClick={() => setPayloadOf(payloadOf === d.id ? null : d.id)}>
                        {payloadOf === d.id ? t("developers.hidePayload") : t("developers.payload")}
                      </button>
                      {d.status !== "succeeded" && d.status !== "pending" && (
                        <button className="sm" onClick={() => retry(d)}><Icon name="refresh" size="xs" />{t("developers.retryNow")}</button>
                      )}
                    </span>
                  </td>
                </tr>
                {payloadOf === d.id && (
                  <tr>
                    <td colSpan={5}>
                      <pre className="code payload">{payload.data ? JSON.stringify(payload.data.payload, null, 2) : t("common.loading")}</pre>
                    </td>
                  </tr>
                )}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
