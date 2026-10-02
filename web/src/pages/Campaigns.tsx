import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers, useTags, useTemplates } from "../api/hooks";
import type { AudienceCounts, Campaign, CampaignStatus, Page, Recipient, RecipientStatus } from "../api/types";
import { CONTACT_FIELDS, campaignSupported, fieldToken, previewBody, templateSlots } from "../lib/campaigns";

const STATUS_PILL: Record<CampaignStatus, string> = {
  draft: "",
  scheduled: "t-pending",
  running: "t-pending",
  paused: "t-pending",
  completed: "t-approved",
  cancelled: "",
  failed: "t-rejected",
};

const RECIPIENT_STATUSES: RecipientStatus[] = ["pending", "skipped", "queued", "sent", "delivered", "read", "failed"];

const active = (s: CampaignStatus) => s === "scheduled" || s === "running" || s === "paused";

export default function Campaigns() {
  const { t } = useTranslation();
  const role = useMe().data?.tenant?.role;
  const canManage = role === "owner" || role === "admin";
  const [creating, setCreating] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);

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
  const campaigns = list.data?.pages.flatMap((p) => p.data) ?? [];

  if (role && !canManage) {
    return (
      <section>
        <h1>{t("campaigns.title")}</h1>
        <div className="card muted">{t("campaigns.ownersOnly")}</div>
      </section>
    );
  }

  return (
    <section>
      <div className="page-head">
        <h1>{t("campaigns.title")}</h1>
        <div className="actions">
          <button className="primary" onClick={() => setCreating(!creating)}>{t("campaigns.new")}</button>
        </div>
      </div>
      {creating && <NewCampaign onDone={(id) => { setCreating(false); setSelected(id); }} />}

      <div className={`contacts-layout ${selected ? "has-detail" : ""}`}>
        <div className="card table-wrap">
          {list.isLoading && <div className="muted">{t("common.loading")}</div>}
          {!list.isLoading && campaigns.length === 0 && <div className="muted">{t("campaigns.empty")}</div>}
          {campaigns.length > 0 && (
            <table className="clickable">
              <thead>
                <tr>
                  <th>{t("campaigns.name")}</th>
                  <th>{t("campaigns.status")}</th>
                  <th>{t("campaigns.template")}</th>
                  <th>{t("campaigns.progress")}</th>
                  <th>{t("campaigns.when")}</th>
                </tr>
              </thead>
              <tbody>
                {campaigns.map((c) => (
                  <tr key={c.id} className={selected === c.id ? "selected" : ""} onClick={() => setSelected(c.id)}>
                    <td>{c.name}</td>
                    <td><span className={`pill ${STATUS_PILL[c.status]}`}>{t(`campaigns.status_${c.status}`)}</span></td>
                    <td>{c.template.name} <span className="muted small">{c.template.language}</span></td>
                    <td className="nowrap">{c.stats.total ? t("campaigns.progressCell", { ...c.stats }) : "—"}</td>
                    <td className="small">{new Date(c.scheduled_at ?? c.created_at).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {list.hasNextPage && (
            <button className="link" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>{t("contacts.more")}</button>
          )}
        </div>
        {selected && <CampaignDetail id={selected} onClose={() => setSelected(null)} />}
      </div>
    </section>
  );
}

function NewCampaign({ onDone }: { onDone: (id: string) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const numbers = usePhoneNumbers();
  const templates = useTemplates("approved");
  const tags = useTags();
  const connected = (numbers.data ?? []).filter((n) => n.status === "connected");

  const [name, setName] = useState("");
  const [phoneId, setPhoneId] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [vars, setVars] = useState<Record<string, string>>({});
  const [chosenTags, setChosenTags] = useState<string[]>([]);
  const [when, setWhen] = useState<"now" | "later">("now");
  const [scheduledAt, setScheduledAt] = useState("");
  const [rate, setRate] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // One key per form, so a double submit or a retry after a network error creates one campaign.
  const [idemKey] = useState(() => crypto.randomUUID());

  useEffect(() => {
    if (!phoneId && connected.length > 0) setPhoneId(connected[0].id);
  }, [phoneId, connected]);

  const number = connected.find((n) => n.id === phoneId);
  const choices = (templates.data ?? []).filter((tp) => number && tp.whatsapp_account_id === number.whatsapp_account_id);
  const template = choices.find((tp) => tp.id === templateId);
  const slots = useMemo(() => (template ? templateSlots(template.components) : []), [template]);

  const audience = useQuery({
    queryKey: ["campaign-audience", chosenTags],
    queryFn: () => api<AudienceCounts>("POST", "/internal/campaigns/audience", { tags: chosenTags }),
    enabled: chosenTags.length > 0,
  });

  const toggleTag = (name: string) =>
    setChosenTags((cur) => (cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name]));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!template) return;
    setBusy(true);
    setError("");
    try {
      const res = await fetchCreate(
        {
          name,
          phone_number_id: phoneId,
          template: {
            name: template.name,
            language: template.language,
            variables: Object.fromEntries(slots.map((s) => [s.key, vars[s.key] ?? ""])),
          },
          audience: { tags: chosenTags },
          scheduled_at: when === "later" && scheduledAt ? new Date(scheduledAt).toISOString() : undefined,
          send_rate_per_min: rate ? Number(rate) : undefined,
        },
        idemKey,
      );
      await qc.invalidateQueries({ queryKey: ["campaigns"] });
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

  return (
    <form className="card form" onSubmit={submit}>
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
        <div>
          {(tags.data ?? []).map((tg) => (
            <label key={tg.id} className={`chip selectable ${chosenTags.includes(tg.name) ? "on" : ""}`}>
              <input type="checkbox" checked={chosenTags.includes(tg.name)} onChange={() => toggleTag(tg.name)} />
              {tg.name} ({tg.contacts})
            </label>
          ))}
          {tags.data?.length === 0 && <span className="muted small">{t("campaigns.noTags")}</span>}
        </div>
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
        <button className="primary" disabled={busy || !template || chosenTags.length === 0 || audience.data?.eligible === 0}>
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

function CampaignDetail({ id, onClose }: { id: string; onClose: () => void }) {
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

  const cancel = async () => {
    if (!window.confirm(t("campaigns.confirmCancel"))) return;
    setError("");
    try {
      qc.setQueryData(["campaign", id], await api<Campaign>("POST", `/v1/campaigns/${id}/cancel`));
      await qc.invalidateQueries({ queryKey: ["campaigns"] });
      await qc.invalidateQueries({ queryKey: ["campaign-recipients", id] });
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
      {active(c.status) && (
        <div className="detail-actions">
          <button onClick={cancel}>{t("campaigns.cancel")}</button>
        </div>
      )}
      {error && <div className="error">{error}</div>}

      <h3>{t("campaigns.recipients")}</h3>
      <select className="recipient-filter" value={filter} onChange={(e) => setFilter(e.target.value as RecipientStatus | "")}>
        <option value="">{t("campaigns.allRecipients")}</option>
        {RECIPIENT_STATUSES.map((st) => <option key={st} value={st}>{t(`campaigns.r_${st}`)}</option>)}
      </select>
      {c.status === "scheduled" && <div className="muted small">{t("campaigns.notExpanded")}</div>}
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
