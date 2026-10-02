import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { usePhoneNumbers } from "../api/hooks";
import type { CreatedWebhookEndpoint, Page, WebhookDelivery, WebhookEndpoint, WebhookEventType } from "../api/types";

const EVENTS: WebhookEventType[] = ["message.received", "message.status", "template.status", "number.quality", "bot.handoff"];

const PILL: Record<WebhookDelivery["status"], string> = {
  pending: "t-pending",
  retrying: "t-pending",
  succeeded: "t-approved",
  dead: "t-rejected",
};

// The Developers screen's webhook section: endpoints, their signing secret and delivery log.
export default function Webhooks() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const numbers = usePhoneNumbers();
  const endpoints = useQuery({
    queryKey: ["webhook-endpoints"],
    queryFn: async () => (await api<{ data: WebhookEndpoint[] }>("GET", "/v1/webhook-endpoints")).data,
  });
  const [url, setUrl] = useState("https://");
  const [description, setDescription] = useState("");
  const [events, setEvents] = useState<WebhookEventType[]>(EVENTS);
  const [phone, setPhone] = useState("");
  const [created, setCreated] = useState<CreatedWebhookEndpoint | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

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
      setUrl("https://");
      setDescription("");
      await qc.invalidateQueries({ queryKey: ["webhook-endpoints"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
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
      <h2>{t("developers.webhooks")}</h2>
      <p className="muted small">{t("developers.webhooksIntro")}</p>
      {created && (
        <div className="card new-key">
          <strong>{t("developers.secretNow")}</strong>
          <div className="key-row">
            <code>{created.secret}</code>
            <button onClick={() => navigator.clipboard?.writeText(created.secret)}>{t("developers.copy")}</button>
          </div>
        </div>
      )}
      <form className="card inline-form" onSubmit={create}>
        <label className="field">
          {t("developers.url")}
          <input type="url" value={url} onChange={(e) => setUrl(e.target.value)} required pattern="https://.+" />
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
          <button className="primary" disabled={busy || !events.length}>{t("developers.addEndpoint")}</button>
        </div>
      </form>
      {error && <div className="error">{error}</div>}
      {endpoints.data?.length === 0 && <div className="card muted">{t("developers.noEndpoints")}</div>}
      {endpoints.data?.map((ep) => (
        <div key={ep.id} className="card endpoint">
          <div className="endpoint-head">
            <div>
              <code>{ep.url}</code>
              {ep.description && <div className="muted small">{ep.description}</div>}
              <div className="muted small">
                {ep.event_types.map((e) => t(`developers.event_${e}`)).join(", ")} · {numberLabel(ep.phone_number_id)}
              </div>
            </div>
            <div className="actions">
              <button onClick={() => setOpen(open === ep.id ? null : ep.id)}>
                {open === ep.id ? t("developers.hideDeliveries") : t("developers.deliveries")}
              </button>
              <button className="link" onClick={() => remove(ep)}>{t("developers.delete")}</button>
            </div>
          </div>
          {open === ep.id && <Deliveries endpointId={ep.id} />}
        </div>
      ))}
    </>
  );
}

function Deliveries({ endpointId }: { endpointId: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const key = ["webhook-deliveries", endpointId];
  const q = useQuery({
    queryKey: key,
    queryFn: () => api<Page<WebhookDelivery>>("GET", `/v1/webhook-endpoints/${endpointId}/deliveries?limit=50`),
    refetchInterval: 10_000,
  });
  const retry = async (d: WebhookDelivery) => {
    await api("POST", `/v1/webhook-endpoints/${endpointId}/deliveries/${d.id}/retry`);
    await qc.invalidateQueries({ queryKey: key });
  };
  if (!q.data) return <div className="muted small">{t("common.loading")}</div>;
  if (!q.data.data.length) return <div className="muted small">{t("developers.noDeliveries")}</div>;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>{t("developers.event")}</th>
            <th>{t("developers.status")}</th>
            <th>{t("developers.attempts")}</th>
            <th>{t("developers.response")}</th>
            <th>{t("developers.time")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {q.data.data.map((d) => (
            <tr key={d.id}>
              <td>{t(`developers.event_${d.event_type}`)}</td>
              <td><span className={`pill ${PILL[d.status]}`}>{t(`developers.delivery_${d.status}`)}</span></td>
              <td>{d.attempt_count}</td>
              <td className="small">{d.last_response_code ?? d.last_error ?? "—"}</td>
              <td className="small">{new Date(d.created_at).toLocaleString()}</td>
              <td>
                {d.status !== "succeeded" && d.status !== "pending" && (
                  <button className="link" onClick={() => retry(d)}>{t("developers.retry")}</button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
