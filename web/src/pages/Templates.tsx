import { useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, useTemplates } from "../api/hooks";
import type { Template } from "../api/types";

export default function Templates() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canManage = role === "owner" || role === "admin";
  const q = useTemplates();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");

  const sync = async () => {
    setBusy(true);
    setError("");
    setNote("");
    try {
      const r = await api<{ synced: number }>("POST", "/internal/templates/sync");
      setNote(t("templates.synced", { count: r.synced }));
      await qc.invalidateQueries({ queryKey: ["templates"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  const submitDraft = async (tpl: Template) => {
    setError("");
    try {
      await api("POST", `/v1/templates/${tpl.id}/submit`);
      await qc.invalidateQueries({ queryKey: ["templates"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  const remove = async (tpl: Template) => {
    if (!window.confirm(t("templates.confirmDelete", { name: tpl.name }))) return;
    setError("");
    try {
      // A draft never reached Meta, so it is deleted here only.
      if (tpl.status === "draft") await api("DELETE", `/v1/templates/${tpl.id}`);
      else await api("DELETE", `/v1/templates/by-name/${tpl.name}?whatsapp_account_id=${tpl.whatsapp_account_id}`);
      await qc.invalidateQueries({ queryKey: ["templates"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  return (
    <section>
      <div className="page-head">
        <h1>{t("templates.title")}</h1>
        {canManage && (
          <div className="actions">
            <button onClick={sync} disabled={busy}>{busy ? t("templates.syncing") : t("templates.sync")}</button>
            <Link className="button primary" to="/templates/new">{t("templates.new")}</Link>
          </div>
        )}
      </div>
      {note && <div className="card muted">{note}</div>}
      {error && <div className="error">{error}</div>}
      {q.isLoading && <div className="muted">{t("common.loading")}</div>}
      {q.data?.length === 0 && <div className="card muted">{t("templates.empty")}</div>}
      {!!q.data?.length && (
        <div className="card table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("templates.name")}</th>
                <th>{t("templates.language")}</th>
                <th>{t("templates.category")}</th>
                <th>{t("templates.status")}</th>
                <th>{t("templates.body")}</th>
                {canManage && <th />}
              </tr>
            </thead>
            <tbody>
              {q.data.map((tpl) => (
                <tr key={tpl.id}>
                  <td>{tpl.name}</td>
                  <td>{tpl.language}</td>
                  <td>{t(`templates.category_${tpl.category}`)}</td>
                  <td>
                    <span className={`pill t-${tpl.status}`}>{t(`templates.status_${tpl.status}`)}</span>
                    {tpl.rejected_reason && <div className="muted small">{tpl.rejected_reason}</div>}
                  </td>
                  <td className="small">{tpl.components.find((c) => c.type.toUpperCase() === "BODY")?.text}</td>
                  {canManage && (
                    <td>
                      {tpl.status === "draft" && (
                        <button className="link" onClick={() => submitDraft(tpl)}>{t("templates.submitDraft")}</button>
                      )}
                      <button className="link" onClick={() => remove(tpl)}>{t("templates.delete")}</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
