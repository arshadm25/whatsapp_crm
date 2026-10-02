import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe } from "../api/hooks";
import type { ConsentEvent, Contact, ImportResult, Page, Tag } from "../api/types";

const CONSENT_PILL: Record<Contact["opt_in_status"], string> = {
  unknown: "",
  opted_in: "t-approved",
  opted_out: "t-rejected",
};

function displayName(c: Contact) {
  return c.name ?? c.profile_name ?? `+${c.wa_id}`;
}

function useTags() {
  return useQuery({
    queryKey: ["tags"],
    queryFn: async () => (await api<{ data: Tag[] }>("GET", "/internal/contacts/tags")).data,
  });
}

export default function Contacts() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canImport = role === "owner" || role === "admin";
  const tags = useTags();
  const [q, setQ] = useState("");
  const [tag, setTag] = useState("");
  const [consent, setConsent] = useState("");
  const [panel, setPanel] = useState<"add" | "import" | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  const params = new URLSearchParams({ limit: "50" });
  if (q.trim()) params.set("q", q.trim());
  if (tag) params.set("tag", tag);
  if (consent) params.set("opt_in_status", consent);
  const list = useInfiniteQuery({
    queryKey: ["contacts", params.toString()],
    queryFn: ({ pageParam }) =>
      api<Page<Contact>>("GET", `/v1/contacts?${params}${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const contacts = list.data?.pages.flatMap((p) => p.data) ?? [];

  return (
    <section>
      <div className="page-head">
        <h1>{t("contacts.title")}</h1>
        <div className="actions">
          {canImport && <button onClick={() => setPanel(panel === "import" ? null : "import")}>{t("contacts.import")}</button>}
          <button className="primary" onClick={() => setPanel(panel === "add" ? null : "add")}>{t("contacts.add")}</button>
        </div>
      </div>
      {panel === "add" && <AddContact onDone={(id) => { setPanel(null); setSelected(id); }} />}
      {panel === "import" && <ImportContacts />}

      <div className="filters">
        <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("contacts.search")} />
        <select value={tag} onChange={(e) => setTag(e.target.value)}>
          <option value="">{t("contacts.allTags")}</option>
          {tags.data?.map((tg) => (
            <option key={tg.id} value={tg.name}>{tg.name} ({tg.contacts})</option>
          ))}
        </select>
        <select value={consent} onChange={(e) => setConsent(e.target.value)}>
          <option value="">{t("contacts.anyConsent")}</option>
          {(["opted_in", "unknown", "opted_out"] as const).map((s) => (
            <option key={s} value={s}>{t(`contacts.consent_${s}`)}</option>
          ))}
        </select>
      </div>

      <div className={`contacts-layout ${selected ? "has-detail" : ""}`}>
        <div className="card table-wrap">
          {list.isLoading && <div className="muted">{t("common.loading")}</div>}
          {!list.isLoading && contacts.length === 0 && <div className="muted">{t("contacts.empty")}</div>}
          {contacts.length > 0 && (
            <table className="clickable">
              <thead>
                <tr>
                  <th>{t("contacts.name")}</th>
                  <th>{t("contacts.number")}</th>
                  <th>{t("contacts.tags")}</th>
                  <th>{t("contacts.consent")}</th>
                </tr>
              </thead>
              <tbody>
                {contacts.map((c) => (
                  <tr key={c.id} className={selected === c.id ? "selected" : ""} onClick={() => setSelected(c.id)}>
                    <td>{displayName(c)}</td>
                    <td>+{c.wa_id}</td>
                    <td>{c.tags.map((tg) => <span key={tg} className="chip">{tg}</span>)}</td>
                    <td>
                      <span className={`pill ${CONSENT_PILL[c.opt_in_status]}`}>{t(`contacts.consent_${c.opt_in_status}`)}</span>
                      {c.blocked && <span className="pill t-rejected">{t("contacts.blocked")}</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {list.hasNextPage && (
            <button className="link" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>{t("contacts.more")}</button>
          )}
        </div>
        {selected && <ContactDetail id={selected} onClose={() => setSelected(null)} />}
      </div>
    </section>
  );
}

function AddContact({ onDone }: { onDone: (id: string) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [waId, setWaId] = useState("");
  const [name, setName] = useState("");
  const [tagText, setTagText] = useState("");
  const [optedIn, setOptedIn] = useState(false);
  const [evidence, setEvidence] = useState("");
  const [error, setError] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      const c = await api<Contact>("POST", "/v1/contacts", {
        wa_id: waId.replace(/[^0-9]/g, ""),
        name: name || undefined,
        tags: tagText.split(",").map((s) => s.trim()).filter(Boolean),
        opt_in: optedIn ? { status: "opted_in", source: "dashboard", evidence } : undefined,
      });
      await qc.invalidateQueries({ queryKey: ["contacts"] });
      await qc.invalidateQueries({ queryKey: ["tags"] });
      onDone(c.id);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  return (
    <form className="card form" onSubmit={submit}>
      <div className="row">
        <label className="field">
          {t("contacts.number")}
          <input value={waId} onChange={(e) => setWaId(e.target.value)} required inputMode="tel" placeholder="919876543210" />
          <span className="muted small">{t("contacts.numberHint")}</span>
        </label>
        <label className="field">
          {t("contacts.name")}
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={200} />
        </label>
      </div>
      <label className="field">
        {t("contacts.tags")}
        <input value={tagText} onChange={(e) => setTagText(e.target.value)} placeholder="vip, wholesale" />
        <span className="muted small">{t("contacts.tagsHint")}</span>
      </label>
      <label className="check">
        <input type="checkbox" checked={optedIn} onChange={(e) => setOptedIn(e.target.checked)} />
        {t("contacts.optedInNow")}
      </label>
      {optedIn && (
        <label className="field">
          {t("contacts.evidence")}
          <input value={evidence} onChange={(e) => setEvidence(e.target.value)} required placeholder={t("contacts.evidenceHint")} />
        </label>
      )}
      {error && <div className="error">{error}</div>}
      <div className="actions">
        <button className="primary">{t("contacts.save")}</button>
      </div>
    </form>
  );
}

function ImportContacts() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [cc, setCc] = useState("91");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!file) return;
    setBusy(true);
    setError("");
    setResult(null);
    try {
      const form = new FormData();
      form.append("file", file);
      form.append("default_country_code", cc);
      if (confirmed) form.append("consent_confirmed", "true");
      setResult(await api<ImportResult>("POST", "/internal/contacts/import", form));
      await qc.invalidateQueries({ queryKey: ["contacts"] });
      await qc.invalidateQueries({ queryKey: ["tags"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="card form" onSubmit={submit}>
      <div className="row">
        <label className="field">
          {t("contacts.file")}
          <input type="file" accept=".csv,text/csv" onChange={(e) => setFile(e.target.files?.[0] ?? null)} required />
        </label>
        <label className="field">
          {t("contacts.countryCode")}
          <input value={cc} onChange={(e) => setCc(e.target.value)} maxLength={4} inputMode="numeric" />
        </label>
      </div>
      <span className="muted small">{t("contacts.fileHint")}</span>
      <label className="check">
        <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
        {t("contacts.confirmConsent")}
      </label>
      {error && <div className="error">{error}</div>}
      {result && (
        <div className="muted">
          {t("contacts.importResult", { ...result })}
          {result.errors.length > 0 && (
            <ul className="small">
              {result.errors.map((e) => <li key={e.line}>{t("contacts.line", { ...e })}</li>)}
            </ul>
          )}
        </div>
      )}
      <div className="actions">
        <button className="primary" disabled={busy || !file}>{busy ? t("contacts.importing") : t("contacts.import")}</button>
      </div>
    </form>
  );
}

function ContactDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const contact = useQuery({ queryKey: ["contact", id], queryFn: () => api<Contact>("GET", `/v1/contacts/${id}`) });
  const history = useQuery({
    queryKey: ["consent", id],
    queryFn: async () => (await api<{ data: ConsentEvent[] }>("GET", `/internal/contacts/${id}/consent`)).data,
  });
  const [draft, setDraft] = useState<{ name: string; language: string } | null>(null);
  const [newTag, setNewTag] = useState("");
  const [evidence, setEvidence] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");

  const c = contact.data;
  if (!c) return <aside className="card contact-detail muted">{t("common.loading")}</aside>;
  const form = draft ?? { name: c.name ?? "", language: c.language ?? "" };

  const patch = async (body: Record<string, unknown>) => {
    setError("");
    setNote("");
    try {
      const updated = await api<Contact>("PATCH", `/v1/contacts/${id}`, body);
      qc.setQueryData(["contact", id], updated);
      await qc.invalidateQueries({ queryKey: ["contacts"] });
      await qc.invalidateQueries({ queryKey: ["tags"] });
      await qc.invalidateQueries({ queryKey: ["consent", id] });
      return true;
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
      return false;
    }
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await patch({ name: form.name, language: form.language })) {
      setDraft(null);
      setNote(t("contacts.saved"));
    }
  };
  const addTag = async (e: FormEvent) => {
    e.preventDefault();
    if (newTag.trim() && (await patch({ add_tags: [newTag.trim()] }))) setNewTag("");
  };
  const recordConsent = async (status: "opted_in" | "opted_out") => {
    if (await patch({ consent: { status, source: "dashboard", evidence } })) setEvidence("");
  };
  const fields = Object.entries(c.custom_fields ?? {});

  return (
    <aside className="card contact-detail">
      <div className="detail-head">
        <div>
          <h2>{displayName(c)}</h2>
          <div className="muted">+{c.wa_id}{c.profile_name && c.name ? ` · ${c.profile_name}` : ""}</div>
        </div>
        <button className="link" onClick={onClose}>{t("contacts.close")}</button>
      </div>

      <form className="form" onSubmit={save}>
        <label className="field">
          {t("contacts.name")}
          <input value={form.name} onChange={(e) => setDraft({ ...form, name: e.target.value })} maxLength={200} />
        </label>
        <label className="field">
          {t("contacts.language")}
          <input value={form.language} onChange={(e) => setDraft({ ...form, language: e.target.value })} placeholder="en" />
          <span className="muted small">{t("contacts.languageHint")}</span>
        </label>
        <div className="actions">
          {note && <span className="muted small">{note}</span>}
          <button className="primary" disabled={!draft}>{t("contacts.save")}</button>
        </div>
      </form>

      <h3>{t("contacts.tags")}</h3>
      <div>
        {c.tags.map((tg) => (
          <span key={tg} className="chip">
            {tg}
            <button className="chip-x" aria-label="remove" onClick={() => patch({ remove_tags: [tg] })}>×</button>
          </span>
        ))}
      </div>
      <form className="inline-add" onSubmit={addTag}>
        <input value={newTag} onChange={(e) => setNewTag(e.target.value)} placeholder={t("contacts.addTag")} maxLength={50} />
        <button disabled={!newTag.trim()}>{t("contacts.addTag")}</button>
      </form>

      {fields.length > 0 && (
        <>
          <h3>{t("contacts.customFields")}</h3>
          <dl className="fields">
            {fields.map(([k, v]) => (
              <div key={k}><dt>{k}</dt><dd>{String(v)}</dd></div>
            ))}
          </dl>
        </>
      )}

      <h3>{t("contacts.history")}</h3>
      <div>
        <span className={`pill ${CONSENT_PILL[c.opt_in_status]}`}>{t(`contacts.consent_${c.opt_in_status}`)}</span>
        {c.blocked && <span className="pill t-rejected">{t("contacts.blocked")}</span>}
      </div>
      <input className="evidence" value={evidence} onChange={(e) => setEvidence(e.target.value)} placeholder={t("contacts.evidenceHint")} />
      <div className="detail-actions">
        <button onClick={() => recordConsent("opted_in")} disabled={!evidence.trim()}>{t("contacts.markOptedIn")}</button>
        <button onClick={() => recordConsent("opted_out")}>{t("contacts.markOptedOut")}</button>
        <button onClick={() => patch({ blocked: !c.blocked })}>{c.blocked ? t("contacts.unblock") : t("contacts.block")}</button>
      </div>
      {error && <div className="error">{error}</div>}
      {history.data?.length === 0 && <div className="muted small">{t("contacts.noHistory")}</div>}
      <ul className="history">
        {history.data?.map((h, i) => (
          <li key={i}>
            <strong>{t(`contacts.kind_${h.kind}`)}</strong> · {t(`contacts.source_${h.source}`)}
            {h.recorded_by_name && <> · {t("contacts.by", { name: h.recorded_by_name })}</>}
            <div className="muted small">
              {new Date(h.occurred_at).toLocaleString()}
              {h.evidence && <> · {h.evidence}</>}
            </div>
          </li>
        ))}
      </ul>
    </aside>
  );
}
