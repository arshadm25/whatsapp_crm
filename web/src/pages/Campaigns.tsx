import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate } from "react-router-dom";
import type { TFunction } from "i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers, useTags, useTemplates } from "../api/hooks";
import type { AudienceCounts, Campaign, CampaignStatus, Page, Recipient, RecipientStatus } from "../api/types";
import {
  CONTACT_FIELDS, campaignSupported, estimateSend, fieldToken, previewBody, previewText, templateSlots, type SendEstimate,
} from "../lib/campaigns";
import Icon, { type IconName } from "../components/Icon";
import { dailyLimit } from "../lib/numbers";
import { initials } from "../lib/time";

// The server's rate when a campaign leaves it empty (campaigns.DefaultRate).
const DEFAULT_RATE = 1000;

const STATUS_PILL: Record<CampaignStatus, string> = {
  draft: "",
  scheduled: "inf",
  running: "wa",
  paused: "wa",
  completed: "ok",
  cancelled: "",
  failed: "er",
};
const STATUS_ICON: Record<CampaignStatus, { icon: IconName; tone: string }> = {
  draft: { icon: "file", tone: "gy" },
  scheduled: { icon: "calendar", tone: "am" },
  running: { icon: "send", tone: "" },
  paused: { icon: "clock", tone: "am" },
  completed: { icon: "check", tone: "" },
  cancelled: { icon: "x", tone: "gy" },
  failed: { icon: "alert", tone: "rd" },
};

const RECIPIENT_STATUSES: RecipientStatus[] = ["pending", "skipped", "queued", "sent", "delivered", "read", "failed"];

const active = (s: CampaignStatus) => s === "scheduled" || s === "running" || s === "paused";

type Tab = "all" | "scheduled" | "sending" | "completed" | "draft";
const IN_TAB: Record<Tab, (s: CampaignStatus) => boolean> = {
  all: () => true,
  scheduled: (s) => s === "scheduled",
  sending: (s) => s === "running" || s === "paused",
  completed: (s) => s === "completed",
  draft: (s) => s === "draft",
};
const TAB_STATUSES: Record<Tab, CampaignStatus[]> = {
  all: [],
  scheduled: ["scheduled"],
  sending: ["running", "paused"],
  completed: ["completed"],
  draft: ["draft"],
};

// Recipients a message went out to: sent, delivered, read, or failed after sending.
const attempted = (c: Campaign) => c.stats.sent + c.stats.delivered + c.stats.read + c.stats.failed;
const pct = (n: number, of: number) => (of > 0 ? `${Math.round((n * 100) / of)}%` : "—");

export default function Campaigns() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canManage = role === "owner" || role === "admin";
  const location = useLocation();
  const navigate = useNavigate();
  // Contacts picked on the Contacts page arrive here as the new campaign's audience.
  const [pickedIds, setPickedIds] = useState<string[] | null>(
    () => (location.state as { contactIds?: string[] } | null)?.contactIds ?? null,
  );
  const [creating, setCreating] = useState(() => pickedIds !== null);
  const [editing, setEditing] = useState<Campaign | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("all");
  const [search, setSearch] = useState("");
  useEffect(() => {
    if (location.state) navigate(location.pathname, { replace: true, state: null });
  }, [location, navigate]);

  // Counts per status across every campaign, not only the loaded page.
  const counts = useQuery({
    queryKey: ["campaigns", "counts"],
    queryFn: () => api<Record<CampaignStatus | "all", number>>("GET", "/internal/campaigns/counts"),
    enabled: canManage,
  });
  const tabCount = (tb: Tab) =>
    counts.data
      ? tb === "all" ? counts.data.all : TAB_STATUSES[tb].reduce((n, st) => n + (counts.data[st] ?? 0), 0)
      : all.filter((c) => IN_TAB[tb](c.status)).length;

  const list = useInfiniteQuery({
    queryKey: ["campaigns"],
    queryFn: ({ pageParam }) =>
      api<Page<Campaign>>("GET", `/v1/campaigns?limit=25${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: canManage,
    // Running campaigns' counts change as messages go out.
    refetchInterval: (q) => (q.state.data?.pages.some((p) => p.data.some((c) => active(c.status))) ? 5000 : false),
  });
  const all = list.data?.pages.flatMap((p) => p.data) ?? [];
  const campaigns = all.filter((c) => IN_TAB[tab](c.status));
  const sent = all.reduce((n, c) => n + attempted(c), 0);
  const delivered = all.reduce((n, c) => n + c.stats.delivered + c.stats.read, 0);
  const read = all.reduce((n, c) => n + c.stats.read, 0);
  const scheduled = all.filter((c) => c.status === "scheduled" && c.scheduled_at).sort((a, b) => a.scheduled_at!.localeCompare(b.scheduled_at!));

  if (role && !canManage) {
    return (
      <section>
        <h1>{t("campaigns.title")}</h1>
        <div className="card muted">{t("campaigns.ownersOnly")}</div>
      </section>
    );
  }

  const needle = search.trim().toLowerCase();
  const shown = needle ? campaigns.filter((c) => c.name.toLowerCase().includes(needle) || c.template.name.includes(needle)) : campaigns;
  const composing = creating || !!editing;
  const none = !list.isLoading && !list.isError && all.length === 0;
  const openNew = () => { setPickedIds(null); setEditing(null); setSelected(null); setCreating(true); };
  const closeForm = () => { setCreating(false); setEditing(null); setPickedIds(null); };

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{composing ? (editing ? t("campaigns.editTitle", { name: editing.name }) : t("campaigns.new")) : t("campaigns.title")}</h1>
          <p className="sub">{composing ? t("campaigns.formIntro") : t("campaigns.intro")}</p>
        </div>
        <div className="actions">
          {composing ? (
            <button type="button" onClick={closeForm}><Icon name="x" size="s" />{t("campaigns.backToList")}</button>
          ) : (
            !none && <button className="primary" onClick={openNew}><Icon name="plus" size="s" />{t("campaigns.new")}</button>
          )}
        </div>
      </div>

      {composing ? (
        <NewCampaign
          key={editing?.id ?? "new"}
          editing={editing}
          contactIds={editing ? editing.audience.contact_ids ?? null : pickedIds}
          onClearContacts={() => setPickedIds(null)}
          onDone={(id) => { closeForm(); setSelected(id); }}
          onCancel={closeForm}
        />
      ) : none ? (
        <FirstCampaign onStart={openNew} />
      ) : (
      <div className="stack">
        {all.length > 0 && (
          <div className="grid g4">
            <div className="card stat">
              <div className="sh"><span className="sl">{t("campaigns.kpiSent")}</span><span className="ic"><Icon name="send" size="s" /></span></div>
              <span className="sv">{sent.toLocaleString()}</span>
              <span className="sf">{t("campaigns.kpiAcross", { count: all.length })}</span>
            </div>
            <div className="card stat">
              <div className="sh"><span className="sl">{t("campaigns.kpiDelivery")}</span><span className="ic"><Icon name="check" size="s" /></span></div>
              <span className="sv">{pct(delivered, sent)}</span>
              <span className="sf">{t("campaigns.kpiDeliveryHint")}</span>
            </div>
            <div className="card stat">
              <div className="sh"><span className="sl">{t("campaigns.kpiRead")}</span><span className="ic bl"><Icon name="eye" size="s" /></span></div>
              <span className="sv">{pct(read, sent)}</span>
              <span className="sf">{t("campaigns.kpiReadHint")}</span>
            </div>
            <div className="card stat">
              <div className="sh"><span className="sl">{t("campaigns.kpiScheduled")}</span><span className="ic am"><Icon name="calendar" size="s" /></span></div>
              <span className="sv">{scheduled.length}</span>
              <span className="sf">
                {scheduled[0] ? t("campaigns.kpiNext", { name: scheduled[0].name, when: new Date(scheduled[0].scheduled_at!).toLocaleString() }) : t("campaigns.kpiNone")}
              </span>
            </div>
          </div>
        )}

        <div className={`contacts-layout ${selected ? "has-detail" : ""}`}>
          <div className="card flush">
            <div className="chd">
              <div className="tabs" role="tablist" aria-label={t("campaigns.status")}>
                {(Object.keys(IN_TAB) as Tab[]).map((k) => (
                  <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? "active" : ""} onClick={() => setTab(k)}>
                    {t(`campaigns.tab_${k}`)} <span className="cnt">{tabCount(k)}</span>
                  </button>
                ))}
              </div>
              <label className="iw">
                <Icon name="search" size="s" />
                <input type="search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("campaigns.search")} aria-label={t("campaigns.search")} />
              </label>
            </div>
            {list.isLoading && <div className="cb muted">{t("common.loading")}</div>}
            {list.isError && <div className="cb error">{t("common.error")}</div>}
            {!list.isLoading && all.length > 0 && shown.length === 0 && (
              <div className="cb muted">{needle ? t("campaigns.noMatch") : t("campaigns.emptyTab")}</div>
            )}
            {shown.length > 0 && (
              <div className="table-wrap">
                <table className="clickable">
                  <thead>
                    <tr>
                      <th>{t("campaigns.campaign")}</th>
                      <th>{t("campaigns.audienceCol")}</th>
                      <th>{t("campaigns.status")}</th>
                      <th>{t("campaigns.delivered")}</th>
                      <th>{t("campaigns.read")}</th>
                      <th>{t("campaigns.date")}</th>
                      <th className="r"><span className="sr-only">{t("numbers.actions")}</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {shown.map((c) => {
                      const done = attempted(c);
                      const deliveredPct = done > 0 ? Math.round(((c.stats.delivered + c.stats.read) * 100) / done) : null;
                      return (
                        <tr key={c.id} className={selected === c.id ? "selected" : ""} onClick={() => setSelected(c.id)}>
                          <td>
                            <div className="who">
                              <span className={`ic ${STATUS_ICON[c.status].tone}`}><Icon name={STATUS_ICON[c.status].icon} size="s" /></span>
                              <span><b>{c.name}</b><small className="k">{c.template.name} · {c.template.language}</small></span>
                            </div>
                          </td>
                          <td>
                            {c.audience.tags?.length ? t("campaigns.tagAudience", { tags: c.audience.tags.join(", ") }) : t("campaigns.pickedContacts")}
                            <small className="muted" style={{ display: "block" }}>{t("campaigns.contactsCount", { count: c.stats.total })}</small>
                          </td>
                          <td>
                            <span className={`pill ${STATUS_PILL[c.status]}`}>{t(`campaigns.status_${c.status}`)}</span>
                            {active(c.status) && c.stats.total > 0 && (
                              <small className="muted" style={{ display: "block", marginTop: 4 }}>{t("campaigns.progressCell", { ...c.stats })}</small>
                            )}
                          </td>
                          <td>
                            {deliveredPct === null ? <span className="muted">—</span> : (
                              <div className="bar-cell"><div className="bar"><span style={{ width: `${deliveredPct}%` }} /></div><span className="num-t">{deliveredPct}%</span></div>
                            )}
                          </td>
                          <td className="num-t">{pct(c.stats.read, done)}</td>
                          <td>
                            {c.status === "scheduled" && c.scheduled_at
                              ? new Date(c.scheduled_at).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })
                              : new Date(c.finished_at ?? c.started_at ?? c.created_at).toLocaleDateString([], { day: "numeric", month: "short" })}
                          </td>
                          <td className="r"><button className="lnk">{t("campaigns.report")}</button></td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
            {all.length > 0 && (
              <div className="cf">
                <span>{t("campaigns.showing", { count: shown.length, total: all.length })}</span>
                {list.hasNextPage && (
                  <button className="lnk" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>{t("contacts.more")}</button>
                )}
              </div>
            )}
          </div>
          {selected && (
            <CampaignDetail id={selected} onClose={() => setSelected(null)} onEdit={(c) => { setCreating(false); setSelected(null); setEditing(c); }} />
          )}
        </div>
      </div>
      )}
    </section>
  );
}

// FirstCampaign is the page before any campaign exists: what a campaign needs, with a link to
// each place that sets it up, and what is already done.
function FirstCampaign({ onStart }: { onStart: () => void }) {
  const { t } = useTranslation();
  const numbers = usePhoneNumbers();
  const templates = useTemplates();
  const tags = useTags();
  const hasNumber = (numbers.data ?? []).some((n) => n.status === "connected");
  const hasTemplate = (templates.data ?? []).some((tp) => tp.status === "approved");
  const hasTags = (tags.data ?? []).some((tg) => tg.contacts > 0);
  const steps: { key: string; done: boolean; to: string }[] = [
    { key: "number", done: hasNumber, to: "/numbers/connect" },
    { key: "template", done: hasTemplate, to: "/templates/new" },
    { key: "tags", done: hasTags, to: "/contacts" },
  ];
  return (
    <div className="card camp-first">
      <span className="camp-first-ic"><Icon name="megaphone" size="lg" /></span>
      <h2>{t("campaigns.firstTitle")}</h2>
      <p className="muted">{t("campaigns.firstText")}</p>
      <ol className="camp-setup">
        {steps.map((s, i) => (
          <li key={s.key} className={s.done ? "done" : ""}>
            <span className="dot">{s.done ? <Icon name="check" size="xs" /> : i + 1}</span>
            <span className="txt"><b>{t(`campaigns.first_${s.key}`)}</b><small>{t(`campaigns.first_${s.key}Hint`)}</small></span>
            {s.done ? <span className="pill ok">{t("campaigns.firstDone")}</span> : <Link className="button sm" to={s.to}>{t(`campaigns.first_${s.key}Go`)}</Link>}
          </li>
        ))}
      </ol>
      <button className="primary" onClick={onStart} disabled={!hasNumber}><Icon name="plus" size="s" />{t("campaigns.new")}</button>
    </div>
  );
}

// localInput formats a time for a datetime-local input.
function localInput(iso: string) {
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}

function NewCampaign({ editing, onDone, onCancel, contactIds, onClearContacts }: {
  editing: Campaign | null;
  onDone: (id: string) => void;
  onCancel: () => void;
  contactIds: string[] | null;
  onClearContacts: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const me = useMe().data;
  const numbers = usePhoneNumbers();
  const allTemplates = useTemplates();
  const tags = useTags();
  const connected = (numbers.data ?? []).filter((n) => n.status === "connected");

  const [name, setName] = useState(editing?.name ?? "");
  const [phoneId, setPhoneId] = useState(editing?.phone_number_id ?? "");
  const [templateId, setTemplateId] = useState(editing?.template_id ?? "");
  const [vars, setVars] = useState<Record<string, string>>(editing?.template.variables ?? {});
  const [chosenTags, setChosenTags] = useState<string[]>(editing?.audience.tags ?? []);
  const [when, setWhen] = useState<"now" | "later">(editing?.scheduled_at ? "later" : "now");
  const [scheduledAt, setScheduledAt] = useState(editing?.scheduled_at ? localInput(editing.scheduled_at) : "");
  const [rate, setRate] = useState(editing ? String(editing.send_rate_per_min) : "");
  const [reviewing, setReviewing] = useState(false);
  const reviewRef = useRef<HTMLDivElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // One key per form, so a double submit or a retry after a network error creates one campaign.
  const [idemKey] = useState(() => crypto.randomUUID());

  useEffect(() => {
    if (!phoneId && connected.length > 0) setPhoneId(connected[0].id);
  }, [phoneId, connected]);
  // Bring the review summary into view when it opens below a long form.
  useEffect(() => {
    if (reviewing) reviewRef.current?.scrollIntoView?.({ behavior: "smooth", block: "nearest" });
  }, [reviewing]);

  const number = connected.find((n) => n.id === phoneId);
  // Drafts may use a template still waiting for Meta's review; sending needs it approved.
  const choices = (allTemplates.data ?? []).filter(
    (tp) => number && tp.whatsapp_account_id === number.whatsapp_account_id && (tp.status === "approved" || tp.status === "pending" || tp.status === "draft"),
  );
  const approved = choices.filter((tp) => tp.status === "approved");
  const waiting = choices.filter((tp) => tp.status !== "approved");
  const template = choices.find((tp) => tp.id === templateId);
  const slots = useMemo(() => (template ? templateSlots(template.components) : []), [template]);

  const audience = useQuery({
    queryKey: ["campaign-audience", chosenTags, contactIds],
    queryFn: () =>
      api<AudienceCounts>("POST", "/internal/campaigns/audience", contactIds ? { contact_ids: contactIds } : { tags: chosenTags }),
    enabled: chosenTags.length > 0 || !!contactIds?.length,
  });
  const hasAudience = chosenTags.length > 0 || !!contactIds?.length;
  const counts = hasAudience ? audience.data : undefined;

  const toggleTag = (name: string) =>
    setChosenTags((cur) => (cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name]));

  const limit = dailyLimit(number?.messaging_limit_tier);
  const perMin = rate ? Number(rate) : DEFAULT_RATE;
  const estimate = counts ? estimateSend(counts.eligible, perMin, limit) : null;

  const save = async (draft: boolean) => {
    if (!template) return;
    setBusy(true);
    setError("");
    try {
      const body = {
        name,
        phone_number_id: phoneId,
        template: {
          name: template.name,
          language: template.language,
          variables: Object.fromEntries(slots.map((s) => [s.key, vars[s.key] ?? ""])),
        },
        audience: contactIds ? { contact_ids: contactIds } : { tags: chosenTags },
        scheduled_at: when === "later" && scheduledAt ? new Date(scheduledAt).toISOString() : undefined,
        send_rate_per_min: rate ? Number(rate) : undefined,
        draft,
      };
      const res = editing
        ? await api<Campaign>("PUT", `/v1/campaigns/${editing.id}`, body)
        : await fetchCreate(body, `${idemKey}-${draft ? "draft" : "send"}`);
      await qc.invalidateQueries({ queryKey: ["campaigns"] });
      await qc.invalidateQueries({ queryKey: ["campaign", res.id] });
      onDone(res.id);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
      setReviewing(false);
    } finally {
      setBusy(false);
    }
  };

  if (numbers.data && connected.length === 0) {
    return (
      <div className="card camp-first">
        <span className="camp-first-ic"><Icon name="phone" size="lg" /></span>
        <h2>{t("campaigns.noNumberTitle")}</h2>
        <p className="muted">{t("campaigns.noNumber")}</p>
        <Link className="button primary" to="/numbers/connect">{t("campaigns.first_numberGo")}</Link>
      </div>
    );
  }

  // What still stops the campaign from sending, shown as a checklist beside the form.
  const valuesFilled = slots.every((s) => (vars[s.key] ?? "").trim() !== "");
  const checks: { key: string; ok: boolean }[] = [
    { key: "name", ok: name.trim() !== "" },
    { key: "template", ok: !!template && template.status === "approved" },
    { key: "values", ok: !!template && valuesFilled },
    { key: "audience", ok: hasAudience && (counts?.eligible ?? 0) > 0 },
    { key: "when", ok: when === "now" || scheduledAt !== "" },
  ];
  const ready = checks.every((c) => c.ok);
  const passed = checks.filter((c) => c.ok).length;
  const canSend = !busy && !!template && template.status === "approved" && hasAudience && audience.data?.eligible !== 0;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!reviewing) {
      setReviewing(true);
      return;
    }
    void save(false);
  };

  const header = template?.components.find((c) => c.type.toUpperCase() === "HEADER");
  const headerFormat = (header?.format ?? "TEXT").toUpperCase();
  const footer = template?.components.find((c) => c.type.toUpperCase() === "FOOTER")?.text;
  const buttons = template?.components.find((c) => c.type.toUpperCase() === "BUTTONS")?.buttons ?? [];
  const skipped = counts ? counts.total - counts.eligible : 0;
  const whenText = when === "later" && scheduledAt ? new Date(scheduledAt).toLocaleString() : t("campaigns.rightAway");

  return (
    <div className="split camp-compose">
      <form className="card flush tpl" onSubmit={submit}>
        <fieldset className="camp-fs" disabled={reviewing || busy}>
        <div className="sec">
          <h3><span className="stepn">1</span>{t("campaigns.stepBasics")}</h3>
          <div className="grid g2">
            <label className="field">
              {t("campaigns.name")}
              <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} placeholder={t("campaigns.namePlaceholder")} />
              <span className="hint">{t("campaigns.nameHint")}</span>
            </label>
            <label className="field">
              {t("campaigns.number")}
              <select value={phoneId} onChange={(e) => { setPhoneId(e.target.value); setTemplateId(""); }}>
                {connected.map((n) => (
                  <option key={n.id} value={n.id}>{n.display_phone_number}{n.verified_name ? ` · ${n.verified_name}` : ""}</option>
                ))}
              </select>
              {number && (
                <span className="hint">
                  {limit < 0 ? t("campaigns.limitNone") : t("campaigns.limitPlain", { limit: limit.toLocaleString() })}{" "}
                  <Link to="/numbers/limits">{t("campaigns.limitLink")}</Link>
                </span>
              )}
            </label>
          </div>
        </div>

        <div className="sec">
          <h3><span className="stepn">2</span>{t("campaigns.stepMessage")}</h3>
          {number && allTemplates.data && choices.length === 0 ? (
            <div className="camp-callout">
              <Icon name="file" size="s" />
              <div>
                <b>{t("campaigns.noTemplatesTitle")}</b>
                <span>{t("campaigns.noTemplatesText")}</span>
              </div>
              <Link className="button sm" to="/templates/new">{t("campaigns.first_templateGo")}</Link>
            </div>
          ) : (
            <label className="field">
              {t("campaigns.template")}
              <select value={templateId} onChange={(e) => { setTemplateId(e.target.value); setVars({}); }} required>
                <option value="">{t("campaigns.pickTemplate")}</option>
                {approved.length > 0 && (
                  <optgroup label={t("campaigns.groupApproved")}>
                    {approved.map((tp) => (
                      <option key={tp.id} value={tp.id} disabled={!campaignSupported(tp.components)}>
                        {tp.name} ({tp.language}) · {t(`templates.category_${tp.category}`)}
                      </option>
                    ))}
                  </optgroup>
                )}
                {waiting.length > 0 && (
                  <optgroup label={t("campaigns.groupWaiting")}>
                    {waiting.map((tp) => (
                      <option key={tp.id} value={tp.id} disabled={!campaignSupported(tp.components)}>
                        {tp.name} ({tp.language}) · {t(`templates.status_${tp.status}`)}
                      </option>
                    ))}
                  </optgroup>
                )}
              </select>
              {template && template.status !== "approved" ? (
                <span className="hint warn-t">{t("campaigns.templateWaiting")}</span>
              ) : approved.length === 0 && waiting.length > 0 ? (
                <span className="hint">{t("campaigns.onlyWaiting")}</span>
              ) : (
                <span className="hint">{t("campaigns.templateHint")} <Link to="/templates">{t("campaigns.manageTemplates")}</Link></span>
              )}
            </label>
          )}

          {template && slots.length > 0 && (
            <div className="samples">
              <div className="sl"><b>{t("campaigns.variables")}</b></div>
              <span className="hint">{t("campaigns.variablesHint")}</span>
              {slots.map((s) => (
                <div key={s.key} className="var-row">
                  <label className="field">
                    {t(`campaigns.slot_${s.part}`, { key: s.key.replace(/^(header|button)\.?/, "") })}
                    <input
                      value={vars[s.key] ?? ""}
                      onChange={(e) => setVars({ ...vars, [s.key]: e.target.value })}
                      required
                      type={s.media ? "url" : "text"}
                      placeholder={s.media ? `https://…/${s.media}` : fieldToken("first_name", "there")}
                    />
                  </label>
                  {!s.media && (
                    <select
                      aria-label={t("campaigns.insertField")}
                      value=""
                      onChange={(e) => e.target.value && setVars({ ...vars, [s.key]: (vars[s.key] ?? "") + fieldToken(e.target.value) })}
                    >
                      <option value="">{t("campaigns.insertField")}</option>
                      {CONTACT_FIELDS.map((f) => <option key={f} value={f}>{t(`campaigns.field_${f}`)}</option>)}
                    </select>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="sec">
          <h3><span className="stepn">3</span>{t("campaigns.stepAudience")}</h3>
          {contactIds ? (
            <div className="camp-picked">
              <span className="chip">{t("campaigns.pickedCount", { count: contactIds.length })}</span>
              {!editing && <button type="button" className="lnk" onClick={onClearContacts}>{t("campaigns.useTags")}</button>}
            </div>
          ) : tags.data && tags.data.length === 0 ? (
            <div className="camp-callout">
              <Icon name="tag" size="s" />
              <div>
                <b>{t("campaigns.noTagsTitle")}</b>
                <span>{t("campaigns.noTagsText")}</span>
              </div>
              <Link className="button sm" to="/contacts">{t("campaigns.first_tagsGo")}</Link>
            </div>
          ) : (
            <div className="field">
              {t("campaigns.audience")}
              <div className="camp-tags">
                {(tags.data ?? []).map((tg) => (
                  <label key={tg.id} className={`chip selectable ${chosenTags.includes(tg.name) ? "on" : ""}`}>
                    <input type="checkbox" checked={chosenTags.includes(tg.name)} onChange={() => toggleTag(tg.name)} />
                    {tg.name} <span className="muted">{tg.contacts.toLocaleString()}</span>
                  </label>
                ))}
              </div>
              <span className="hint">{t("campaigns.audienceHint")}</span>
            </div>
          )}
          {hasAudience && (
            <div className="camp-reach" aria-live="polite">
              {audience.isLoading ? (
                <span className="muted">{t("campaigns.counting")}</span>
              ) : counts ? (
                <>
                  <div className="camp-reach-main">
                    <b>{counts.eligible.toLocaleString()}</b>
                    <span>{t("campaigns.willReceive", { count: counts.eligible })}</span>
                  </div>
                  {skipped > 0 && (
                    <div className="camp-reach-skip">
                      <span>{t("campaigns.skippedTotal", { count: skipped })}</span>
                      <ul>
                        {counts.no_opt_in > 0 && <li>{t("campaigns.skipNoOptIn", { count: counts.no_opt_in })}</li>}
                        {counts.opted_out > 0 && <li>{t("campaigns.skipOptedOut", { count: counts.opted_out })}</li>}
                        {counts.blocked > 0 && <li>{t("campaigns.skipBlocked", { count: counts.blocked })}</li>}
                      </ul>
                    </div>
                  )}
                  {counts.total > 0 && counts.eligible === 0 && <span className="hint warn-t">{t("campaigns.nobodyEligible")}</span>}
                </>
              ) : null}
            </div>
          )}
        </div>

        <div className="sec last">
          <h3><span className="stepn">4</span>{t("campaigns.stepWhen")}</h3>
          <div className="field">
            {t("campaigns.when")}
            <div className="segmented" role="radiogroup" aria-label={t("campaigns.when")}>
              <button type="button" role="radio" aria-checked={when === "now"} className={when === "now" ? "on" : ""} onClick={() => setWhen("now")}>{t("campaigns.sendNow")}</button>
              <button type="button" role="radio" aria-checked={when === "later"} className={when === "later" ? "on" : ""} onClick={() => setWhen("later")}>{t("campaigns.sendLater")}</button>
            </div>
            {when === "later" && (
              <input type="datetime-local" className="camp-when" aria-label={t("campaigns.sendLater")} value={scheduledAt} onChange={(e) => setScheduledAt(e.target.value)} required />
            )}
          </div>
          <details className="camp-adv" open={!!rate && rate !== String(DEFAULT_RATE)}>
            <summary>{t("campaigns.advanced")}</summary>
            <label className="field">
              {t("campaigns.rate")}
              <input type="number" min={1} max={10000} value={rate} onChange={(e) => setRate(e.target.value)} placeholder={String(DEFAULT_RATE)} />
              <span className="hint">{t("campaigns.rateHelp", { rate: DEFAULT_RATE })}</span>
            </label>
          </details>
        </div>
        </fieldset>

        {reviewing && (
          <div ref={reviewRef} className="sec camp-review" role="region" aria-label={t("campaigns.reviewTitle")}>
            <h3><Icon name="check" size="s" />{t("campaigns.reviewTitle")}</h3>
            <dl>
              <div><dt>{t("campaigns.number")}</dt><dd>{number ? `${number.display_phone_number}${number.verified_name ? ` · ${number.verified_name}` : ""}` : "—"}</dd></div>
              <div><dt>{t("campaigns.template")}</dt><dd>{template ? `${template.name} (${template.language})` : "—"}</dd></div>
              <div><dt>{t("campaigns.reviewReach")}</dt><dd>{counts ? t("campaigns.reviewReachValue", { count: counts.eligible, skipped }) : "—"}</dd></div>
              <div><dt>{t("campaigns.when")}</dt><dd>{whenText}</dd></div>
              {estimate && <div><dt>{t("campaigns.reviewTime")}</dt><dd>{estimateText(t, estimate, limit)}</dd></div>}
            </dl>
            <p className="hint">{t("campaigns.reviewNote")}</p>
          </div>
        )}

        {error && <div className="error camp-error">{error}</div>}
        <div className="camp-actions">
          {reviewing ? (
            <>
              <button type="button" onClick={() => setReviewing(false)} disabled={busy}>{t("campaigns.backToEdit")}</button>
              <button className="primary" disabled={!canSend}>
                {busy ? t("common.loading") : when === "later" ? t("campaigns.confirmSchedule") : t("campaigns.confirmSend")}
              </button>
            </>
          ) : (
            <>
              <button type="button" onClick={onCancel}>{t("common.cancel")}</button>
              <span className="end-group">
                <button type="button" disabled={busy || !template || !name.trim()} onClick={() => save(true)}>{t("campaigns.saveDraft")}</button>
                <button className="primary" disabled={!canSend}>{t("campaigns.reviewBtn")}</button>
              </span>
            </>
          )}
        </div>
      </form>

      <aside className="stack">
        <div className="card flush">
          <div className="chd"><h2>{t("campaigns.preview")}</h2></div>
          <div className="cb phone-wrap">
            <div className="phone">
              <div className="phone-head">
                <span className="av">{initials(number?.verified_name || me?.tenant?.name || "")}</span>
                <span><b>{number?.verified_name || me?.tenant?.name}</b><span>{t("templates.businessAccount")}</span></span>
              </div>
              <div className="phone-body">
                {template ? (
                  <>
                    <div className="pbubble">
                      {header && headerFormat === "TEXT" && header.text && <span className="ph">{previewText(header.text, vars, "header.")}</span>}
                      {header && headerFormat !== "TEXT" && <span className="ph muted">{t(`templates.header_${headerFormat}`, { defaultValue: headerFormat })}</span>}
                      {previewBody(template.components, vars)}
                      {footer && <span className="pf">{footer}</span>}
                      <span className="pt">{new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span>
                    </div>
                    {buttons.map((b, i) => <div key={i} className="pbtn">{b.text}</div>)}
                  </>
                ) : (
                  <div className="pbubble muted">{t("campaigns.previewEmpty")}</div>
                )}
              </div>
            </div>
          </div>
          {template && slots.some((s) => !s.media) && <div className="cf"><span>{t("campaigns.previewHint")}</span></div>}
        </div>
        <div className="card flush">
          <div className="chd">
            <div><h2>{t("campaigns.checks")}</h2></div>
            <span className={`pill ${ready ? "ok" : "wa"}`}>{t("templates.checksCount", { passed, total: checks.length })}</span>
          </div>
          <ul className="lst cb">
            {checks.map((c) => (
              <li key={c.key} className={c.ok ? "ok" : "no"}><Icon name={c.ok ? "check" : "x"} size="s" />{t(`campaigns.check_${c.key}`)}</li>
            ))}
          </ul>
          {estimate && <div className="cf"><span><Icon name="clock" size="xs" /> {estimateText(t, estimate, limit)}</span></div>}
        </div>
      </aside>
    </div>
  );
}

function estimateText(t: TFunction, e: SendEstimate, limit: number): string {
  if (e.kind === "days") return t("campaigns.estimateDays", { count: e.value, limit: limit.toLocaleString() });
  return t("campaigns.estimateMinutes", { count: e.value });
}

// POST /v1/campaigns with an Idempotency-Key header, which the shared api() helper does not set.
async function fetchCreate(body: unknown, key: string): Promise<Campaign> {
  const m = document.cookie.match(/(?:^|;\s*)ecogo_csrf=([^;]+)/);
  const res = await fetch("/v1/campaigns", {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
      "Idempotency-Key": key,
      "X-CSRF-Token": m ? decodeURIComponent(m[1]) : "",
    },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const e = data?.error ?? {};
    throw new ApiError(res.status, e.code ?? "error", e.message ?? res.statusText, e.param);
  }
  return data as Campaign;
}

function CampaignDetail({ id, onClose, onEdit }: { id: string; onClose: () => void; onEdit: (c: Campaign) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [filter, setFilter] = useState<RecipientStatus | "">("");
  const [error, setError] = useState("");
  const campaign = useQuery({
    queryKey: ["campaign", id],
    queryFn: () => api<Campaign>("GET", `/v1/campaigns/${id}`),
    refetchInterval: (q) => (q.state.data && active(q.state.data.status) ? 5000 : false),
  });
  const recipients = useInfiniteQuery({
    queryKey: ["campaign-recipients", id, filter],
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams({ limit: "50" });
      if (filter) qs.set("status", filter);
      if (pageParam) qs.set("cursor", pageParam);
      return api<Page<Recipient>>("GET", `/internal/campaigns/${id}/recipients?${qs}`);
    },
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });

  const c = campaign.data;
  if (!c) return <aside className="card contact-detail muted">{t("common.loading")}</aside>;

  const act = async (action: "cancel" | "pause" | "resume") => {
    if (action === "cancel" && !window.confirm(t("campaigns.confirmCancel"))) return;
    setError("");
    try {
      qc.setQueryData(["campaign", id], await api<Campaign>("POST", `/v1/campaigns/${id}/${action}`));
      await qc.invalidateQueries({ queryKey: ["campaigns"] });
      await qc.invalidateQueries({ queryKey: ["campaign-recipients", id] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };
  const remove = async () => {
    if (!window.confirm(t("campaigns.confirmDelete"))) return;
    setError("");
    try {
      await api("DELETE", `/v1/campaigns/${id}`);
      await qc.invalidateQueries({ queryKey: ["campaigns"] });
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  const s = c.stats;
  const rows = recipients.data?.pages.flatMap((p) => p.data) ?? [];
  return (
    <aside className="card contact-detail">
      <div className="detail-head">
        <div>
          <h2>{c.name}</h2>
          <div className="muted">
            {c.template.name} ({c.template.language}) · {(c.audience.tags ?? []).join(", ")}
          </div>
        </div>
        <button className="link" onClick={onClose}>{t("contacts.close")}</button>
      </div>
      <div>
        <span className={`pill ${STATUS_PILL[c.status]}`}>{t(`campaigns.status_${c.status}`)}</span>
        <span className="muted small"> {t("campaigns.rateValue", { rate: c.send_rate_per_min })}</span>
      </div>
      {c.status === "scheduled" && c.scheduled_at && (
        <div className="muted small">{t("campaigns.startsAt", { at: new Date(c.scheduled_at).toLocaleString() })}</div>
      )}
      <dl className="stats">
        {(["total", "skipped", "queued", "sent", "delivered", "read", "failed"] as const).map((k) => (
          <div key={k}><dt>{t(`campaigns.stat_${k}`)}</dt><dd>{s[k]}</dd></div>
        ))}
      </dl>
      {(active(c.status) || c.status === "draft") && (
        <div className="detail-actions">
          {(c.status === "draft" || c.status === "scheduled") && <button onClick={() => onEdit(c)}>{t("campaigns.edit")}</button>}
          {c.status === "running" && <button onClick={() => act("pause")}>{t("campaigns.pause")}</button>}
          {c.status === "paused" && <button className="primary" onClick={() => act("resume")}>{t("campaigns.resume")}</button>}
          {active(c.status) && <button onClick={() => act("cancel")}>{t("campaigns.cancel")}</button>}
          {c.status === "draft" && <button className="bdg" onClick={remove}>{t("campaigns.delete")}</button>}
        </div>
      )}
      {error && <div className="error">{error}</div>}

      <h3>{t("campaigns.recipients")}</h3>
      <select className="recipient-filter" value={filter} onChange={(e) => setFilter(e.target.value as RecipientStatus | "")}>
        <option value="">{t("campaigns.allRecipients")}</option>
        {RECIPIENT_STATUSES.map((st) => <option key={st} value={st}>{t(`campaigns.r_${st}`)}</option>)}
      </select>
      {(c.status === "scheduled" || c.status === "draft") && <div className="muted small">{t("campaigns.notExpanded")}</div>}
      <ul className="history">
        {rows.map((r) => (
          <li key={r.contact_id}>
            <strong>{r.name ?? `+${r.wa_id}`}</strong> · {t(`campaigns.r_${r.status}`)}
            {r.skip_reason && <> · {t(`campaigns.skip_${r.skip_reason}`, { defaultValue: r.skip_reason })}</>}
            {r.error_code && <> · {t("campaigns.errorCode", { code: r.error_code })}</>}
          </li>
        ))}
      </ul>
      {recipients.hasNextPage && (
        <button className="link" onClick={() => recipients.fetchNextPage()} disabled={recipients.isFetchingNextPage}>{t("contacts.more")}</button>
      )}
    </aside>
  );
}
