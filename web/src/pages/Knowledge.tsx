import { useState } from "react";
import type { FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type { AITestResult, AIUsage, KnowledgeSource } from "../api/types";

type Kind = "faq" | "text" | "website" | "document";
const KINDS: Kind[] = ["faq", "text", "website", "document"];
const STATUS_PILL = { pending: "t-pending", ready: "t-approved", failed: "t-rejected" } as const;

const errorText = (err: unknown, fallback: string) => (err instanceof ApiError ? err.message : fallback);

export default function Knowledge() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canManage = role === "owner" || role === "admin";
  const qc = useQueryClient();
  const [error, setError] = useState("");

  const sources = useQuery({
    queryKey: ["knowledge"],
    enabled: canManage,
    queryFn: async () => (await api<{ data: KnowledgeSource[] }>("GET", "/v1/knowledge/sources")).data,
    // A downloading page turns ready in a few seconds.
    refetchInterval: (q) => (q.state.data?.some((s) => s.status === "pending") ? 3000 : false),
  });
  const usage = useQuery({
    queryKey: ["ai-usage"],
    enabled: canManage,
    queryFn: () => api<AIUsage>("GET", "/v1/ai/usage"),
  });

  if (!canManage) {
    return (
      <section>
        <div className="page-head"><h1>{t("knowledge.title")}</h1></div>
        <div className="muted">{t("knowledge.ownersOnly")}</div>
      </section>
    );
  }

  const changed = () => {
    qc.invalidateQueries({ queryKey: ["knowledge"] });
    qc.invalidateQueries({ queryKey: ["ai-usage"] });
  };
  const act = async (fn: () => Promise<unknown>) => {
    setError("");
    try {
      await fn();
      changed();
    } catch (err) {
      setError(errorText(err, t("common.error")));
    }
  };
  const u = usage.data;

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("knowledge.title")}</h1>
          <p className="sub">{t("knowledge.lead")}</p>
        </div>
      </div>
      {u && !u.configured && <div className="card notice">{t("knowledge.notConfigured")}</div>}
      {u && (u.limit === 0 ? (
        <div className="card notice">{t("knowledge.noAllowance")}</div>
      ) : (
        <p className="small">{t("knowledge.usage", { used: u.used, limit: u.limit })}</p>
      ))}
      {error && <div className="error">{error}</div>}

      <h3>{t("knowledge.sources")}</h3>
      <div className="card table-wrap">
        {sources.isLoading && <div className="muted">{t("common.loading")}</div>}
        {!sources.isLoading && (sources.data ?? []).length === 0 && <div className="muted">{t("knowledge.empty")}</div>}
        {(sources.data ?? []).length > 0 && (
          <table>
            <tbody>
              {sources.data!.map((s) => (
                <tr key={s.id}>
                  <td>
                    {s.title} <span className="pill">{t(`knowledge.kind_${s.kind}`)}</span>
                    {s.url && <div className="muted small">{s.url}</div>}
                    {s.error && <div className="danger-text small">{s.error}</div>}
                  </td>
                  <td>
                    <span className={`pill ${STATUS_PILL[s.status]}`}>{t(`knowledge.status_${s.status}`)}</span>
                    <div className="muted small">{t("knowledge.passages", { count: s.chunk_count })}</div>
                  </td>
                  <td className="actions">
                    {s.kind === "website" && s.status !== "pending" && (
                      <button className="link" onClick={() => act(() => api("POST", `/v1/knowledge/sources/${s.id}/refresh`))}>{t("knowledge.refresh")}</button>
                    )}
                    <button
                      className="link"
                      onClick={() => {
                        if (window.confirm(t("knowledge.confirmDelete"))) act(() => api("DELETE", `/v1/knowledge/sources/${s.id}`));
                      }}
                    >
                      {t("knowledge.delete")}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <AddSource onDone={changed} />
      <TestBox />
    </section>
  );
}

function AddSource({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const [kind, setKind] = useState<Kind>("faq");
  const [title, setTitle] = useState("");
  const [items, setItems] = useState([{ question: "", answer: "" }]);
  const [text, setText] = useState("");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      if (kind === "document") {
        const form = new FormData();
        if (file) form.append("file", file);
        await api("POST", "/v1/knowledge/sources/upload", form);
      } else {
        const body =
          kind === "faq"
            ? { kind, title, items: items.filter((i) => i.question.trim() && i.answer.trim()) }
            : kind === "text"
              ? { kind, title, text }
              : { kind, title: title || undefined, url };
        await api("POST", "/v1/knowledge/sources", body);
      }
      setTitle("");
      setItems([{ question: "", answer: "" }]);
      setText("");
      setUrl("");
      setFile(null);
      onDone();
    } catch (err) {
      setError(errorText(err, t("common.error")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h3>{t("knowledge.add")}</h3>
      <form className="card form" onSubmit={submit}>
        <div className="tabs" role="tablist">
          {KINDS.map((k) => (
            <button type="button" key={k} role="tab" aria-selected={kind === k} className={kind === k ? "active" : ""} onClick={() => setKind(k)}>
              {t(`knowledge.kind_${k}`)}
            </button>
          ))}
        </div>
        {(kind === "faq" || kind === "text") && (
          <label className="field">
            {t("knowledge.titleLabel")}
            <input value={title} maxLength={200} required onChange={(e) => setTitle(e.target.value)} />
          </label>
        )}
        {kind === "faq" && (
          <>
            {items.map((it, i) => (
              <div className="row" key={i}>
                <label className="field">
                  {t("knowledge.question")}
                  <input value={it.question} onChange={(e) => setItems(items.map((x, j) => (j === i ? { ...x, question: e.target.value } : x)))} />
                </label>
                <label className="field">
                  {t("knowledge.answer")}
                  <input value={it.answer} onChange={(e) => setItems(items.map((x, j) => (j === i ? { ...x, answer: e.target.value } : x)))} />
                </label>
              </div>
            ))}
            <button type="button" className="link" onClick={() => setItems([...items, { question: "", answer: "" }])}>{t("knowledge.addQuestion")}</button>
          </>
        )}
        {kind === "text" && (
          <label className="field">
            {t("knowledge.text")}
            <textarea rows={6} value={text} required onChange={(e) => setText(e.target.value)} />
          </label>
        )}
        {kind === "website" && (
          <label className="field">
            {t("knowledge.url")}
            <input type="url" value={url} placeholder="https://" required onChange={(e) => setUrl(e.target.value)} />
            <span className="muted small">{t("knowledge.urlHelp")}</span>
          </label>
        )}
        {kind === "document" && (
          <label className="field">
            {t("knowledge.file")}
            <input type="file" accept=".txt,.md,.csv,.html,.htm" required onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            <span className="muted small">{t("knowledge.fileHelp")}</span>
          </label>
        )}
        {error && <div className="error">{error}</div>}
        <div className="actions">
          <button className="primary" disabled={busy}>{t("knowledge.save")}</button>
        </div>
      </form>
    </>
  );
}

function TestBox() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [question, setQuestion] = useState("");
  const [result, setResult] = useState<AITestResult | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const ask = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      setResult(await api<AITestResult>("POST", "/v1/ai/test", { question }));
      qc.invalidateQueries({ queryKey: ["ai-usage"] });
    } catch (err) {
      setError(errorText(err, t("common.error")));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h3>{t("knowledge.testTitle")}</h3>
      <form className="card form" onSubmit={ask}>
        <div className="ask-row">
          <label className="field">
            <input aria-label={t("knowledge.testTitle")} value={question} maxLength={1000} placeholder={t("knowledge.testPlaceholder")} onChange={(e) => setQuestion(e.target.value)} />
          </label>
          <button className="primary" disabled={busy || !question.trim()}>{t("knowledge.ask")}</button>
        </div>
        {error && <div className="error">{error}</div>}
        {result && (
          <div>
            {result.answered ? (
              <p>{result.text}</p>
            ) : (
              <p className="muted">{t(`knowledge.reason_${result.reason ?? "error"}`)}</p>
            )}
            {result.confidence > 0 && <div className="muted small">{t("knowledge.confidence", { value: result.confidence.toFixed(2) })}</div>}
            {result.answered && (result.sources ?? []).length > 0 && (
              <div className="muted small">{t("knowledge.usedSources")}: {[...new Set(result.sources!.map((s) => s.title))].join(", ")}</div>
            )}
          </div>
        )}
      </form>
    </>
  );
}
