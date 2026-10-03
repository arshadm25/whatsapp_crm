import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers, useTags, useTemplates } from "../api/hooks";
import type { AudienceCounts, Campaign, CampaignStatus, Page, Recipient, RecipientStatus } from "../api/types";
import { CONTACT_FIELDS, campaignSupported, fieldToken, previewBody, templateSlots } from "../lib/campaigns";
import Icon, { type IconName } from "../components/Icon";
import { highestTier, tierLabel } from "../lib/numbers";

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
  const numbers = usePhoneNumbers();
  const connected = (numbers.data ?? []).filter((n) => n.status === "connected");
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
  const tier = highestTier(connected.map((n) => n.messaging_limit_tier));

  return (
    <section>
      <div className="page-head">
        <div>
          <h1>{t("campaigns.title")}</h1>
          <p className="sub">{t("campaigns.intro")}</p>
        </div>
        <div className="actions">
          <button className="primary" onClick={() => { setPickedIds(null); setEditing(null); setCreating(!creating); }}><Icon name="plus" size="s" />{t("campaigns.new")}</button>
        </div>
      </div>
      {(creating || editing) && (
        <NewCampaign
          key={editing?.id ?? "new"}
          editing={editing}
          contactIds={editing ? editing.audience.contact_ids ?? null : pickedIds}
          onClearContacts={() => setPickedIds(null)}
          onDone={(id) => { setCreating(false); setEditing(null); setPickedIds(null); setSelected(id); }}
          onCancel={() => { setCreating(false); setEditing(null); }}
        />
      )}

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
            {!list.isLoading && shown.length === 0 && <div className="cb muted">{t("campaigns.empty")}</div>}
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
            <CampaignDetail id={selected} onClose={() => setSelected(null)} onEdit={(c) => { setCreating(false); setEditing(c); }} />
          )}
        </div>
        {tier && (
          <div className="banner" style={{ margin: 0 }}>
            <Icon name="alert" size="s" />
            <div>
              <b>{t("campaigns.pacedTitle")}</b>
              <span>{t("campaigns.pacedText", { limit: tierLabel(tier, t("numbers.unlimited")) })}</span>
            </div>
          </div>
        )}
      </div>
    </section>
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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // One key per form, so a double submit or a retry after a network error creates one campaign.
  const [idemKey] = useState(() => crypto.randomUUID());

  useEffect(() => {
    if (!phoneId && connected.length > 0) setPhoneId(connected[0].id);
  }, [phoneId, connected]);

  const number = connected.find((n) => n.id === phoneId);
  // Drafts may use a template still waiting for Meta's review; sending needs it approved.
  const choices = (allTemplates.data ?? []).filter(
    (tp) => number && tp.whatsapp_account_id === number.whatsapp_account_id && (tp.status === "approved" || tp.status === "pending" || tp.status === "draft"),
  );
  const template = choices.find((tp) => tp.id === templateId);
  const slots = useMemo(() => (template ? templateSlots(template.components) : []), [template]);

  const audience = useQuery({
    queryKey: ["campaign-audience", chosenTags, contactIds],
    queryFn: () =>
      api<AudienceCounts>("POST", "/internal/campaigns/audience", contactIds ? { contact_ids: contactIds } : { tags: chosenTags }),
    enabled: chosenTags.length > 0 || !!contactIds?.length,
  });

  const toggleTag = (name: string) =>
    setChosenTags((cur) => (cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name]));

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
    } finally {
      setBusy(false);
    }
  };

  if (numbers.data && connected.length === 0) {
    return <div className="card muted">{t("campaigns.noNumber")}</div>;
  }

  const submit = (e: FormEvent) => {
    e.preventDefault();
    void save(false);
  };

  return (
    <form className="card form" onSubmit={submit}>
      {editing && <h2>{t("campaigns.editTitle", { name: editing.name })}</h2>}
      <div className="row">
        <label className="field">
          {t("campaigns.name")}
          <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} placeholder={t("campaigns.namePlaceholder")} />
        </label>
        <label className="field">
          {t("campaigns.number")}
          <select value={phoneId} onChange={(e) => { setPhoneId(e.target.value); setTemplateId(""); }}>
            {connected.map((n) => (
              <option key={n.id} value={n.id}>{n.display_phone_number}{n.verified_name ? ` · ${n.verified_name}` : ""}</option>
            ))}
          </select>
          {number && (
            <span className="muted small">{t("campaigns.limitHint", { tier: number.messaging_limit_tier ?? "TIER_250" })}</span>
          )}
        </label>
      </div>

      <label className="field">
        {t("campaigns.template")}
        <select value={templateId} onChange={(e) => { setTemplateId(e.target.value); setVars({}); }} required>
          <option value="">{t("campaigns.pickTemplate")}</option>
          {choices.map((tp) => (
            <option key={tp.id} value={tp.id} disabled={!campaignSupported(tp.components)}>
              {tp.name} ({tp.language}) · {t(`templates.category_${tp.category}`)}
              {tp.status !== "approved" && ` · ${t(`templates.status_${tp.status}`)}`}
            </option>
          ))}
        </select>
        {number && choices.length === 0 && <span className="muted small">{t("campaigns.noTemplates")}</span>}
      </label>

      {template && slots.length > 0 && (
        <fieldset className="vars">
          <legend>{t("campaigns.variables")}</legend>
          <span className="muted small">{t("campaigns.variablesHint")}</span>
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
          <div className="preview small">{previewBody(template.components, vars)}</div>
        </fieldset>
      )}

      <div className="field">
        {t("campaigns.audience")}
        {contactIds ? (
          <div>
            <span className="chip">{t("campaigns.pickedCount", { count: contactIds.length })}</span>
            {!editing && <button type="button" className="link" onClick={onClearContacts}>{t("campaigns.useTags")}</button>}
          </div>
        ) : (
        <div>
          {(tags.data ?? []).map((tg) => (
            <label key={tg.id} className={`chip selectable ${chosenTags.includes(tg.name) ? "on" : ""}`}>
              <input type="checkbox" checked={chosenTags.includes(tg.name)} onChange={() => toggleTag(tg.name)} />
              {tg.name} ({tg.contacts})
            </label>
          ))}
          {tags.data?.length === 0 && <span className="muted small">{t("campaigns.noTags")}</span>}
        </div>
        )}
        {audience.data && (
          <span className="muted small">{t("campaigns.audienceCounts", { ...audience.data })}</span>
        )}
      </div>

      <div className="row">
        <div className="field">
          {t("campaigns.when")}
          <label className="check">
            <input type="radio" checked={when === "now"} onChange={() => setWhen("now")} /> {t("campaigns.sendNow")}
          </label>
          <label className="check">
            <input type="radio" checked={when === "later"} onChange={() => setWhen("later")} /> {t("campaigns.sendLater")}
          </label>
          {when === "later" && (
            <input type="datetime-local" value={scheduledAt} onChange={(e) => setScheduledAt(e.target.value)} required />
          )}
        </div>
        <label className="field">
          {t("campaigns.rate")}
          <input type="number" min={1} max={10000} value={rate} onChange={(e) => setRate(e.target.value)} placeholder="1000" />
          <span className="muted small">{t("campaigns.rateHint")}</span>
        </label>
      </div>

      {error && <div className="error">{error}</div>}
      <div className="actions">
        <button type="button" onClick={onCancel}>{t("common.cancel")}</button>
        <button type="button" disabled={busy || !template || !name.trim()} onClick={() => save(true)}>{t("campaigns.saveDraft")}</button>
        <button
          className="primary"
          disabled={busy || !template || template.status !== "approved" || (chosenTags.length === 0 && !contactIds?.length) || audience.data?.eligible === 0}
        >
          {when === "later" ? t("campaigns.schedule") : t("campaigns.start")}
        </button>
      </div>
    </form>
  );
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
