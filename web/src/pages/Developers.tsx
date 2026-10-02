import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers } from "../api/hooks";
import type { APIKey, CreatedAPIKey } from "../api/types";
import Webhooks from "../components/Webhooks";

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
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [created, setCreated] = useState<CreatedAPIKey | null>(null);
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const k = await api<CreatedAPIKey>("POST", "/internal/developers/api-keys", { name, phone_number_id: phone || null });
      setCreated(k);
      setCopied(false);
      setName("");
      setPhone("");
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
  const when = (iso: string | null) => (iso ? new Date(iso).toLocaleString() : t("developers.never"));

  if (!allowed) return <section><h1>{t("developers.title")}</h1><div className="card muted">{t("developers.noAccess")}</div></section>;

  return (
    <section>
      <div className="page-head">
        <h1>{t("developers.title")}</h1>
      </div>
      <p className="muted">{t("developers.intro")}</p>

      <h2>{t("developers.keys")}</h2>
      {created && (
        <div className="card new-key">
          <strong>{t("developers.copyNow")}</strong>
          <div className="key-row">
            <code>{created.key}</code>
            <button onClick={copy}>{copied ? t("developers.copied") : t("developers.copy")}</button>
          </div>
          <div className="muted small">{t("developers.usage")}</div>
        </div>
      )}
      <form className="card inline-form" onSubmit={create}>
        <label className="field">
          {t("developers.keyName")}
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={80} required placeholder={t("developers.keyNameHint")} />
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
        <div className="actions">
          <button className="primary" disabled={busy || !name.trim()}>{t("developers.create")}</button>
        </div>
      </form>
      {error && <div className="error">{error}</div>}
      {keys.data?.length === 0 && <div className="card muted">{t("developers.noKeys")}</div>}
      {!!keys.data?.length && (
        <div className="card table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("developers.keyName")}</th>
                <th>{t("developers.key")}</th>
                <th>{t("developers.number")}</th>
                <th>{t("developers.lastUsed")}</th>
                <th>{t("developers.created")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {keys.data.map((k) => (
                <tr key={k.id} className={k.revoked_at ? "muted" : ""}>
                  <td>{k.name}</td>
                  <td><code>{k.prefix}…</code></td>
                  <td>{numberLabel(k.phone_number_id)}</td>
                  <td>{when(k.last_used_at)}</td>
                  <td>{when(k.created_at)}</td>
                  <td>
                    {k.revoked_at ? (
                      <span className="pill t-deleted">{t("developers.revoked")}</span>
                    ) : (
                      <button className="link" onClick={() => revoke(k)}>{t("developers.revoke")}</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Webhooks />
    </section>
  );
}
