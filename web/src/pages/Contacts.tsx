import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useContactSummary, useMe, useTags } from "../api/hooks";
import type { ConsentEvent, Contact, ContactSummary, ImportResult, Page } from "../api/types";
import Icon from "../components/Icon";
import { initials } from "../lib/time";

const CONSENT_PILL: Record<Contact["opt_in_status"], string> = {
  unknown: "wa",
  opted_in: "ok",
  opted_out: "er",
};

// The filter tabs: consent states, and blocked contacts on their own.
type Tab = "" | "opted_in" | "unknown" | "opted_out" | "blocked";
const TABS: { tab: Tab; count: keyof ContactSummary }[] = [
  { tab: "", count: "total" },
  { tab: "opted_in", count: "opted_in" },
  { tab: "unknown", count: "unknown" },
  { tab: "opted_out", count: "opted_out" },
  { tab: "blocked", count: "blocked" },
];

function shortDate(iso: string | null | undefined) {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toDateString() === new Date().toDateString()
    ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleDateString();
}

function displayName(c: Contact) {
  return c.name ?? c.profile_name ?? `+${c.wa_id}`;
}

export default function Contacts() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canImport = role === "owner" || role === "admin";
  const tags = useTags();
  const [q, setQ] = useState("");
  const [tag, setTag] = useState("");
  const [consent, setConsent] = useState<Tab>("");
  const summary = useContactSummary().data;
  const [panel, setPanel] = useState<"add" | "import" | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [bulkTag, setBulkTag] = useState("");
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkError, setBulkError] = useState("");
  const qc = useQueryClient();
  const navigate = useNavigate();

  const params = new URLSearchParams({ limit: "50" });
  if (q.trim()) params.set("q", q.trim());
  if (tag) params.set("tag", tag);
  if (consent === "blocked") params.set("blocked", "true");
  else if (consent) {
    params.set("opt_in_status", consent);
    params.set("blocked", "false");
  }
  const list = useInfiniteQuery({
    queryKey: ["contacts", params.toString()],
    queryFn: ({ pageParam }) =>
      api<Page<Contact> & { total: number }>("GET", `/v1/contacts?${params}${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const contacts = list.data?.pages.flatMap((p) => p.data) ?? [];
  const allPicked = contacts.length > 0 && contacts.every((c) => picked.has(c.id));
  const toggle = (id: string) =>
    setPicked((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  // There is no bulk endpoint, so each picked contact gets its own PATCH.
  const tagPicked = async (e: FormEvent) => {
    e.preventDefault();
    const name = bulkTag.trim();
    if (!name) return;
    setBulkBusy(true);
    setBulkError("");
    try {
      for (const id of picked) await api("PATCH", `/v1/contacts/${id}`, { add_tags: [name] });
      setBulkTag("");
      setPicked(new Set());
    } catch (e) {
      setBulkError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBulkBusy(false);
      await qc.invalidateQueries({ queryKey: ["contacts"] });
      await qc.invalidateQueries({ queryKey: ["tags"] });
    }
  };

  const total = list.data?.pages[0]?.total ?? 0;

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("contacts.title")}</h1>
          {summary && <p className="sub">{t("contacts.summary", { count: summary.total, total: summary.total.toLocaleString(), optedIn: summary.opted_in.toLocaleString() })}</p>}
        </div>
        <div className="actions">
          {canImport && <button onClick={() => setPanel(panel === "import" ? null : "import")}><Icon name="upload" size="s" />{t("contacts.import")}</button>}
          <button className="primary" onClick={() => setPanel(panel === "add" ? null : "add")}><Icon name="userPlus" size="s" />{t("contacts.add")}</button>
        </div>
      </div>
      {panel === "add" && <AddContact onDone={(id) => { setPanel(null); setSelected(id); }} />}
      {panel === "import" && <ImportContacts />}

      <div className={`contacts-layout ${selected ? "has-detail" : ""}`}>
        <div className="card flush">
          <div className="tabs" role="tablist" aria-label={t("contacts.consent")}>
            {TABS.map(({ tab: st, count }) => (
              <button key={st || "all"} role="tab" aria-selected={consent === st} className={consent === st ? "active" : ""} onClick={() => { setConsent(st); setPicked(new Set()); }}>
                {st === "" ? t("contacts.allContacts") : st === "blocked" ? t("contacts.blocked") : t(`contacts.consent_${st}`)}
                {summary && <span className="cnt">{summary[count].toLocaleString()}</span>}
              </button>
            ))}
          </div>
          <div className="card-bar">
            <label className="iw">
              <Icon name="search" size="s" />
              <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("contacts.search")} aria-label={t("contacts.search")} />
            </label>
            <select value={tag} onChange={(e) => setTag(e.target.value)} aria-label={t("contacts.allTags")}>
              <option value="">{t("contacts.allTags")}</option>
              {tags.data?.map((tg) => (
                <option key={tg.id} value={tg.name}>{tg.name} ({tg.contacts})</option>
              ))}
            </select>
            <span className="sp" />
            {picked.size > 0 && (
              <>
                <b>{t("contacts.pickedCount", { count: picked.size })}</b>
                <form className="inline-add" onSubmit={tagPicked}>
                  <input value={bulkTag} onChange={(e) => setBulkTag(e.target.value)} placeholder={t("contacts.tagPlaceholder")} aria-label={t("contacts.addTag")} />
                  <button className="sm" disabled={bulkBusy || !bulkTag.trim()}>{t("contacts.addTag")}</button>
                </form>
                {canImport && (
                  <button type="button" className="sm ghost" onClick={() => navigate("/campaigns", { state: { contactIds: [...picked] } })}>
                    <Icon name="megaphone" size="xs" />{t("contacts.startCampaign")}
                  </button>
                )}
                <button type="button" className="link" onClick={() => setPicked(new Set())}>{t("contacts.clearPicked")}</button>
              </>
            )}
          </div>
          {bulkError && <div className="error">{bulkError}</div>}
          {list.isLoading && <div className="cb muted">{t("common.loading")}</div>}
          {!list.isLoading && contacts.length === 0 && <div className="cb muted">{t("contacts.empty")}</div>}
          {contacts.length > 0 && (
            <div className="table-wrap">
              <table className="clickable">
                <thead>
                  <tr>
                    <th style={{ width: 36 }}>
                      <input type="checkbox" aria-label={t("contacts.pickAll")} checked={allPicked}
                        onChange={() => setPicked(allPicked ? new Set() : new Set(contacts.map((c) => c.id)))} />
                    </th>
                    <th>{t("contacts.contact")}</th>
                    <th>{t("contacts.tags")}</th>
                    <th>{t("contacts.optIn")}</th>
                    <th>{t("contacts.optedInAt")}</th>
                    <th>{t("contacts.source")}</th>
                    <th>{t("contacts.lastMessage")}</th>
                    <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
                  </tr>
                </thead>
                <tbody>
                  {contacts.map((c) => (
                    <tr key={c.id} className={selected === c.id ? "selected" : ""} onClick={() => setSelected(c.id)}>
                      <td onClick={(e) => e.stopPropagation()}>
                        <input type="checkbox" aria-label={displayName(c)} checked={picked.has(c.id)} onChange={() => toggle(c.id)} />
                      </td>
                      <td><div className="who"><span className="av">{initials(displayName(c))}</span><span><b>{displayName(c)}</b><small>+{c.wa_id}</small></span></div></td>
                      <td>{c.tags.length ? c.tags.map((tg) => <span key={tg} className="chip">{tg}</span>) : <span className="muted">—</span>}</td>
                      <td>
                        {c.blocked ? (
                          <span className="pill">{t("contacts.blocked")}</span>
                        ) : (
                          <span className={`pill ${CONSENT_PILL[c.opt_in_status]}`}>{t(`contacts.consent_${c.opt_in_status}`)}</span>
                        )}
                      </td>
                      <td className="muted">{c.opted_in_at ? new Date(c.opted_in_at).toLocaleDateString() : "—"}</td>
                      <td className="muted">{c.opt_in_source ? t(`contacts.source_${c.opt_in_source}`) : "—"}</td>
                      <td className="muted">{shortDate(c.last_message_at)}</td>
                      <td className="r"><button className="ib gh sm" aria-label={t("contacts.open")}><Icon name="chevronRight" size="s" /></button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {contacts.length > 0 && (
            <div className="cf">
              <span>{t("contacts.showingOf", { count: contacts.length, total: total.toLocaleString() })}{list.hasNextPage ? ` · ${t("contacts.moreAvailable")}` : ""}</span>
              {list.hasNextPage && (
                <button className="sm" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>{t("contacts.more")}</button>
              )}
            </div>
          )}
        </div>
        {selected && <ContactDetail id={selected} onClose={() => setSelected(null)} />}
      </div>
      <div className="banner ok" style={{ marginTop: 16 }}>
        <Icon name="shield" size="s" />
        <div><b>{t("contacts.optOutTitle")}</b><span>{t("contacts.optOutText")}</span></div>
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
