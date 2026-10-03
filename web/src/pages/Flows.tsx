import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers } from "../api/hooks";
import type { FlowStatus, FlowSubmission, Page, WhatsAppFlow } from "../api/types";

const CATEGORIES = ["SIGN_UP", "SIGN_IN", "APPOINTMENT_BOOKING", "LEAD_GENERATION", "CONTACT_US", "CUSTOMER_SUPPORT", "SURVEY", "OTHER"];

const STATUS_PILL: Record<FlowStatus, string> = {
  draft: "",
  published: "t-approved",
  deprecated: "",
  blocked: "t-rejected",
  throttled: "t-pending",
};

export default function Flows() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canManage = role === "owner" || role === "admin";
  const [tab, setTab] = useState<"flows" | "submissions">("flows");
  const [editing, setEditing] = useState<WhatsAppFlow | "new" | null>(null);
  const qc = useQueryClient();

  const list = useQuery({
    queryKey: ["flows"],
    queryFn: async () => (await api<{ data: WhatsAppFlow[] }>("GET", "/v1/flows")).data,
  });

  return (
    <section>
      <div className="page-head">
        <h1>{t("flows.title")}</h1>
        <div className="actions">
          {canManage && tab === "flows" && <button className="primary" onClick={() => setEditing("new")}>{t("flows.new")}</button>}
        </div>
      </div>
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={tab === "flows"} className={tab === "flows" ? "active" : ""} onClick={() => setTab("flows")}>{t("flows.tabFlows")}</button>
        <button role="tab" aria-selected={tab === "submissions"} className={tab === "submissions" ? "active" : ""} onClick={() => setTab("submissions")}>{t("flows.submissions")}</button>
      </div>

      {tab === "flows" && (
        <>
          {editing && (
            <FlowEditor
              key={editing === "new" ? "new" : editing.id}
              flow={editing === "new" ? null : editing}
              canManage={canManage}
              onClose={() => setEditing(null)}
              onChanged={(f) => {
                qc.invalidateQueries({ queryKey: ["flows"] });
                setEditing(f);
              }}
              onDeleted={() => {
                qc.invalidateQueries({ queryKey: ["flows"] });
                setEditing(null);
              }}
            />
          )}
          <div className="card table-wrap">
            {list.isLoading && <div className="muted">{t("common.loading")}</div>}
            {!list.isLoading && (list.data ?? []).length === 0 && <div className="muted">{t("flows.empty")}</div>}
            {(list.data ?? []).length > 0 && (
              <table className="clickable">
                <thead>
                  <tr><th>{t("flows.name")}</th><th>{t("flows.status")}</th><th>{t("flows.categories")}</th></tr>
                </thead>
                <tbody>
                  {(list.data ?? []).map((f) => (
                    <tr key={f.id} onClick={() => setEditing(f)}>
                      <td>{f.name}</td>
                      <td><span className={`pill ${STATUS_PILL[f.status]}`}>{t(`flows.status_${f.status}`)}</span></td>
                      <td className="small">{f.categories.map((c) => t(`flows.category_${c}`)).join(", ")}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}

      {tab === "submissions" && <Submissions flows={list.data ?? []} />}
    </section>
  );
}

function FlowEditor(props: {
  flow: WhatsAppFlow | null;
  canManage: boolean;
  onClose: () => void;
  onChanged: (f: WhatsAppFlow) => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const { flow } = props;
  const numbers = usePhoneNumbers();
  const accounts = useMemo(() => {
    const m = new Map<string, string[]>();
    for (const n of numbers.data ?? []) {
      if (n.status === "connected") m.set(n.whatsapp_account_id, [...(m.get(n.whatsapp_account_id) ?? []), n.display_phone_number]);
    }
    return [...m.entries()];
  }, [numbers.data]);

  const [name, setName] = useState(flow?.name ?? "");
  const [account, setAccount] = useState(flow?.whatsapp_account_id ?? "");
  const [cats, setCats] = useState<string[]>(flow?.categories ?? ["OTHER"]);
  const [json, setJson] = useState(() => (flow ? JSON.stringify(flow.flow_json, null, 2) : ""));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const editable = props.canManage && (!flow || flow.status === "draft");
  const acct = account || accounts[0]?.[0] || "";

  const run = async (fn: () => Promise<WhatsAppFlow | void>) => {
    setError("");
    setBusy(true);
    try {
      const out = await fn();
      if (out) props.onChanged(out);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  const save = () =>
    run(async () => {
      let parsed: unknown;
      if (json.trim()) {
        try {
          parsed = JSON.parse(json);
        } catch {
          throw new ApiError(400, "invalid_request", t("flows.invalidJson"));
        }
      }
      const body = { whatsapp_account_id: flow ? undefined : acct, name: name.trim(), categories: cats, flow_json: parsed };
      const out = flow ? await api<WhatsAppFlow>("PUT", `/v1/flows/${flow.id}`, body) : await api<WhatsAppFlow>("POST", "/v1/flows", body);
      setSaved(true);
      return out;
    });

  const action = (path: string, confirm?: string) => () => {
    if (confirm && !window.confirm(confirm)) return;
    return run(() => api<WhatsAppFlow>("POST", `/v1/flows/${flow!.id}/${path}`));
  };

  const remove = () => {
    if (!window.confirm(t("flows.confirmDelete", { name: flow!.name }))) return;
    return run(async () => {
      await api("DELETE", `/v1/flows/${flow!.id}`);
      props.onDeleted();
    });
  };

  const toggle = (c: string) => {
    setSaved(false);
    setCats((cur) => (cur.includes(c) ? cur.filter((x) => x !== c) : [...cur, c]));
  };

  return (
    <div className="card flow-editor">
      <div className="page-head">
        <h2>{flow ? t("flows.editing", { name: flow.name }) : t("flows.new")}</h2>
        <div className="actions">
          <button className="link" onClick={props.onClose}>{t("flows.close")}</button>
          {flow && <button className="link" disabled={busy} onClick={action("refresh")}>{t("flows.refresh")}</button>}
          {flow && props.canManage && flow.status === "draft" && (
            <>
              <button className="link" disabled={busy} onClick={remove}>{t("flows.delete")}</button>
              <button disabled={busy || flow.validation_errors.length > 0} onClick={action("publish", t("flows.confirmPublish", { name: flow.name }))}>{t("flows.publish")}</button>
            </>
          )}
          {flow && props.canManage && flow.status === "published" && (
            <button disabled={busy} onClick={action("deprecate", t("flows.confirmDeprecate", { name: flow.name }))}>{t("flows.deprecate")}</button>
          )}
          {editable && <button className="primary" disabled={busy || !name.trim() || (!flow && !acct)} onClick={save}>{saved ? t("flows.saved") : t("flows.save")}</button>}
        </div>
      </div>
      {error && <div className="field-error">{error}</div>}
      {!props.canManage && <div className="muted small">{t("flows.readOnly")}</div>}
      {flow && !editable && props.canManage && <div className="muted small">{t("flows.lockedHelp")}</div>}

      <div className="form">
        <div className="row">
          <label className="field">
            {t("flows.name")}
            <input value={name} maxLength={200} disabled={!editable} placeholder={t("flows.namePlaceholder")} onChange={(e) => { setName(e.target.value); setSaved(false); }} />
          </label>
          {!flow && (
            <label className="field">
              {t("flows.account")}
              <select value={acct} onChange={(e) => setAccount(e.target.value)}>
                {accounts.map(([id, nums]) => <option key={id} value={id}>{nums.join(", ")}</option>)}
              </select>
            </label>
          )}
        </div>
        <fieldset className="field" disabled={!editable}>
          <legend>{t("flows.categories")}</legend>
          <div className="checks">
            {CATEGORIES.map((c) => (
              <label key={c}><input type="checkbox" checked={cats.includes(c)} onChange={() => toggle(c)} /> {t(`flows.category_${c}`)}</label>
            ))}
          </div>
        </fieldset>
        <label className="field">
          {t("flows.json")}
          <textarea
            className="code"
            rows={18}
            spellCheck={false}
            value={json}
            disabled={!editable}
            placeholder={flow ? "" : t("flows.jsonStarter", { defaultValue: "Leave empty to start from a one-screen example." })}
            onChange={(e) => { setJson(e.target.value); setSaved(false); }}
          />
          <span className="muted small">{t("flows.jsonHelp")}</span>
        </label>
      </div>

      {flow && (
        <>
          <h3>{t("flows.errors")}</h3>
          {flow.validation_errors.length === 0 ? (
            <div className="muted">{t("flows.noErrors")}</div>
          ) : (
            <ul className="problems-list">
              {flow.validation_errors.map((e, i) => (
                <li key={i} className="field-error">
                  {e.message}
                  {e.line_start ? <span className="muted small"> (line {e.line_start}{e.column_start ? `, column ${e.column_start}` : ""})</span> : null}
                </li>
              ))}
            </ul>
          )}
          {flow.preview_url && (
            <p>
              <a href={flow.preview_url} target="_blank" rel="noreferrer noopener">{t("flows.preview")}</a>{" "}
              <span className="muted small">{t("flows.previewExpires")}</span>
            </p>
          )}
          {flow.status === "published" && <p className="muted small">{t("flows.sendHelp", { id: flow.meta_flow_id })}</p>}
        </>
      )}
    </div>
  );
}

function Submissions({ flows }: { flows: WhatsAppFlow[] }) {
  const { t } = useTranslation();
  const [flowId, setFlowId] = useState("");
  const list = useInfiniteQuery({
    queryKey: ["flow-submissions", flowId],
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams({ limit: "25" });
      if (flowId) qs.set("flow_id", flowId);
      if (pageParam) qs.set("cursor", pageParam);
      return api<Page<FlowSubmission>>("GET", `/v1/flow-submissions?${qs}`);
    },
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const rows = list.data?.pages.flatMap((p) => p.data) ?? [];
  return (
    <div className="card table-wrap">
      <div className="card-bar">
        <label className="field inline">
          {t("flows.flow")}
          <select value={flowId} onChange={(e) => setFlowId(e.target.value)}>
            <option value="">{t("flows.allFlows")}</option>
            {flows.map((f) => <option key={f.id} value={f.id}>{f.name}</option>)}
          </select>
        </label>
      </div>
      {list.isLoading && <div className="muted">{t("common.loading")}</div>}
      {!list.isLoading && rows.length === 0 && <div className="muted">{t("flows.noSubmissions")}</div>}
      {rows.length > 0 && (
        <table>
          <thead>
            <tr><th>{t("flows.when")}</th><th>{t("flows.contact")}</th><th>{t("flows.flow")}</th><th>{t("flows.answers")}</th></tr>
          </thead>
          <tbody>
            {rows.map((s) => (
              <tr key={s.id}>
                <td className="small nowrap">{new Date(s.created_at).toLocaleString()}</td>
                <td>{s.contact_name ?? s.contact_wa_id} <span className="muted small">{s.contact_wa_id}</span></td>
                <td>{s.flow_name ?? t("flows.unknownFlow")}</td>
                <td className="small">{Object.entries(s.response).map(([k, v]) => `${k}: ${Array.isArray(v) ? v.join(", ") : String(v)}`).join("; ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {list.hasNextPage && <button className="link" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>{t("flows.more")}</button>}
    </div>
  );
}
