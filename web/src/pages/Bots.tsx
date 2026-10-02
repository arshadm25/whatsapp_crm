import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { api, ApiError } from "../api/client";
import { useMe, usePhoneNumbers } from "../api/hooks";
import type { WhatsAppFlow } from "../api/types";
import {
  MAX_BODY,
  MAX_BUTTONS,
  MAX_BUTTON_TITLE,
  NODE_TYPES,
  TRIGGER_TYPES,
  edges,
  emptyFlow,
  layout,
  newNode,
  nextId,
  problems,
  renameNode,
  summary,
  withoutNode,
  type Bot,
  type BotFlow,
  type BotNode,
  type BotSession,
  type BotStatus,
  type BotTrigger,
  type NodeType,
} from "../lib/bots";

const STATUS_PILL: Record<BotStatus, string> = { draft: "", active: "t-approved", paused: "t-pending" };

export default function Bots() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const role = useMe().data?.tenant?.role;
  const canManage = role === "owner" || role === "admin";
  const [editing, setEditing] = useState<Bot | "new" | null>(null);
  const [error, setError] = useState("");
  const numbers = usePhoneNumbers();

  const list = useQuery({
    queryKey: ["bots"],
    queryFn: async () => (await api<{ data: Bot[] }>("GET", "/v1/bots")).data,
    enabled: canManage,
  });

  if (role && !canManage) {
    return (
      <section>
        <h1>{t("bots.title")}</h1>
        <div className="card muted">{t("bots.ownersOnly")}</div>
      </section>
    );
  }

  const act = async (bot: Bot, action: "activate" | "pause" | "delete") => {
    setError("");
    try {
      if (action === "delete") {
        if (!window.confirm(t("bots.confirmDelete", { name: bot.name }))) return;
        await api("DELETE", `/v1/bots/${bot.id}`);
        if (editing !== "new" && editing?.id === bot.id) setEditing(null);
      } else {
        await api("POST", `/v1/bots/${bot.id}/${action}`);
      }
      await qc.invalidateQueries({ queryKey: ["bots"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };

  const numberName = (id: string | null) =>
    id ? (numbers.data ?? []).find((n) => n.id === id)?.display_phone_number ?? "—" : t("bots.allNumbers");

  return (
    <section>
      <div className="page-head">
        <h1>{t("bots.title")}</h1>
        <div className="actions">
          <button className="primary" onClick={() => setEditing("new")}>{t("bots.new")}</button>
        </div>
      </div>
      {error && <div className="field-error">{error}</div>}

      {editing && (
        <BotEditor
          key={editing === "new" ? "new" : editing.id}
          bot={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={(b) => setEditing(b)}
        />
      )}

      <div className="card table-wrap">
        {list.isLoading && <div className="muted">{t("common.loading")}</div>}
        {!list.isLoading && (list.data ?? []).length === 0 && <div className="muted">{t("bots.empty")}</div>}
        {(list.data ?? []).length > 0 && (
          <table>
            <thead>
              <tr>
                <th>{t("bots.name")}</th>
                <th>{t("bots.status")}</th>
                <th>{t("bots.number")}</th>
                <th>{t("bots.triggers")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {(list.data ?? []).map((b) => (
                <tr key={b.id}>
                  <td>{b.name}</td>
                  <td><span className={`pill ${STATUS_PILL[b.status]}`}>{t(`bots.status_${b.status}`)}</span></td>
                  <td>{numberName(b.phone_number_id)}</td>
                  <td className="small">{b.flow.triggers.map((tr) => triggerLabel(tr, t)).join(", ") || "—"}</td>
                  <td className="nowrap">
                    <button className="link" onClick={() => setEditing(b)}>{t("bots.edit")}</button>{" "}
                    {b.status === "active" ? (
                      <button className="link" onClick={() => act(b, "pause")}>{t("bots.pause")}</button>
                    ) : (
                      <button className="link" onClick={() => act(b, "activate")}>{t("bots.activate")}</button>
                    )}{" "}
                    <button className="link" onClick={() => act(b, "delete")}>{t("bots.delete")}</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}

function triggerLabel(tr: BotTrigger, t: TFunction): string {
  if (tr.type === "keyword") return t("bots.triggerKeywordLabel", { words: (tr.keywords ?? []).join(" / ") });
  if (tr.type === "button_reply") return t("bots.triggerButtonLabel", { id: tr.button_id });
  return t(`bots.trigger_${tr.type}`);
}

function BotEditor({ bot, onClose, onSaved }: { bot: Bot | null; onClose: () => void; onSaved: (b: Bot) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const numbers = usePhoneNumbers();
  const [name, setName] = useState(bot?.name ?? "");
  const [phoneId, setPhoneId] = useState(bot?.phone_number_id ?? "");
  const [flow, setFlow] = useState<BotFlow>(() => bot?.flow ?? emptyFlow());
  const [selected, setSelected] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const issues = useMemo(() => problems(flow), [flow]);
  const flowList = useQuery({
    queryKey: ["flows"],
    queryFn: async () => (await api<{ data: WhatsAppFlow[] }>("GET", "/v1/flows")).data,
  });
  const change = (f: BotFlow) => {
    setFlow(f);
    setSaved(false);
  };
  const setNode = (id: string, n: BotNode) => change({ ...flow, nodes: { ...flow.nodes, [id]: n } });

  const add = (type: NodeType) => {
    const id = nextId(flow, type);
    change({ ...flow, nodes: { ...flow.nodes, [id]: newNode(type) }, start: flow.start in flow.nodes ? flow.start : id });
    setSelected(id);
  };

  const save = async () => {
    setError("");
    if (!name.trim()) return setError(t("bots.nameRequired"));
    if (issues.length > 0) return setError(t("bots.fixFirst"));
    setBusy(true);
    try {
      const body = { name: name.trim(), phone_number_id: phoneId || null, flow };
      const out = bot ? await api<Bot>("PUT", `/v1/bots/${bot.id}`, body) : await api<Bot>("POST", "/v1/bots", body);
      await qc.invalidateQueries({ queryKey: ["bots"] });
      setSaved(true);
      onSaved(out);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  const ids = Object.keys(flow.nodes);

  return (
    <div className="card bot-editor">
      <div className="page-head">
        <h2>{bot ? t("bots.editing", { name: bot.name }) : t("bots.new")}</h2>
        <div className="actions">
          <button className="link" onClick={onClose}>{t("bots.close")}</button>
          <button className="primary" disabled={busy} onClick={save}>{saved ? t("bots.saved") : t("bots.save")}</button>
        </div>
      </div>
      {error && <div className="field-error">{error}</div>}

      <div className="form">
        <div className="row">
          <label className="field">
            {t("bots.name")}
            <input value={name} maxLength={100} placeholder={t("bots.namePlaceholder")} onChange={(e) => { setName(e.target.value); setSaved(false); }} />
          </label>
          <label className="field">
            {t("bots.number")}
            <select value={phoneId} onChange={(e) => { setPhoneId(e.target.value); setSaved(false); }}>
              <option value="">{t("bots.allNumbers")}</option>
              {(numbers.data ?? []).filter((n) => n.status === "connected").map((n) => (
                <option key={n.id} value={n.id}>{n.display_phone_number}</option>
              ))}
            </select>
          </label>
        </div>
      </div>

      <h3>{t("bots.triggers")}</h3>
      <p className="muted small">{t("bots.triggersHelp")}</p>
      <Triggers triggers={flow.triggers} onChange={(triggers) => change({ ...flow, triggers })} />

      <h3>{t("bots.map")}</h3>
      <FlowMap flow={flow} selected={selected} onSelect={setSelected} />

      <div className="page-head">
        <h3>{t("bots.nodes")}</h3>
        <label className="field inline">
          <select value="" onChange={(e) => e.target.value && add(e.target.value as NodeType)} aria-label={t("bots.addNode")}>
            <option value="">{t("bots.addNode")}</option>
            {NODE_TYPES.map((n) => <option key={n} value={n}>{t(`bots.node_${n}`)}</option>)}
          </select>
        </label>
      </div>
      <p className="muted small">{t("bots.variablesHelp")}</p>

      {ids.map((id) => (
        <NodeCard
          key={id}
          id={id}
          node={flow.nodes[id]}
          ids={ids}
          isStart={flow.start === id}
          open={selected === id}
          problems={issues.filter((p) => p.node === id).map((p) => p.message)}
          flows={flowList.data ?? []}
          onToggle={() => setSelected(selected === id ? null : id)}
          onChange={(n) => setNode(id, n)}
          onStart={() => change({ ...flow, start: id })}
          onRename={(to) => {
            change(renameNode(flow, id, to));
            setSelected(to);
          }}
          onDelete={() => {
            change(withoutNode(flow, id));
            setSelected(null);
          }}
        />
      ))}

      {issues.length > 0 && (
        <div className="card problems">
          <strong>{t("bots.problems")}</strong>
          <ul>
            {issues.map((p, i) => <li key={i}>{p.node ? <code>{p.node}</code> : null} {p.message}</li>)}
          </ul>
        </div>
      )}

      {bot && <Sessions botId={bot.id} />}
    </div>
  );
}

function Triggers({ triggers, onChange }: { triggers: BotTrigger[]; onChange: (t: BotTrigger[]) => void }) {
  const { t } = useTranslation();
  const set = (i: number, tr: BotTrigger) => onChange(triggers.map((x, j) => (j === i ? tr : x)));
  return (
    <div className="triggers">
      {triggers.map((tr, i) => (
        <div className="trigger-row" key={i}>
          <select
            value={tr.type}
            aria-label={t("bots.triggerType")}
            onChange={(e) => {
              const type = e.target.value as BotTrigger["type"];
              set(i, type === "keyword" ? { type, keywords: [""], match: "exact" } : type === "button_reply" ? { type, button_id: "" } : { type });
            }}
          >
            {TRIGGER_TYPES.map((x) => <option key={x} value={x}>{t(`bots.trigger_${x}`)}</option>)}
          </select>
          {tr.type === "keyword" && (
            <>
              <input
                aria-label={t("bots.keywords")}
                placeholder={t("bots.keywordsPlaceholder")}
                value={(tr.keywords ?? []).join(", ")}
                onChange={(e) => set(i, { ...tr, keywords: e.target.value.split(",").map((k) => k.trimStart()) })}
              />
              <select value={tr.match ?? "exact"} aria-label={t("bots.match")} onChange={(e) => set(i, { ...tr, match: e.target.value as "exact" | "contains" })}>
                <option value="exact">{t("bots.matchExact")}</option>
                <option value="contains">{t("bots.matchContains")}</option>
              </select>
            </>
          )}
          {tr.type === "button_reply" && (
            <input aria-label={t("bots.buttonId")} placeholder={t("bots.buttonIdPlaceholder")} value={tr.button_id ?? ""} onChange={(e) => set(i, { ...tr, button_id: e.target.value })} />
          )}
          <button className="link" onClick={() => onChange(triggers.filter((_, j) => j !== i))}>{t("bots.remove")}</button>
        </div>
      ))}
      {triggers.length < 20 && (
        <button className="link" onClick={() => onChange([...triggers, { type: "keyword", keywords: [""], match: "exact" }])}>{t("bots.addTrigger")}</button>
      )}
    </div>
  );
}

const W = 168;
const H = 56;
const GX = 64;
const GY = 22;

// FlowMap draws the nodes in columns with an arrow for every link.
function FlowMap({ flow, selected, onSelect }: { flow: BotFlow; selected: string | null; onSelect: (id: string) => void }) {
  const { t } = useTranslation();
  const placed = useMemo(() => layout(flow), [flow]);
  const pos = new Map(placed.map((p) => [p.id, { x: p.col * (W + GX) + 8, y: p.row * (H + GY) + 8 }]));
  const cols = Math.max(0, ...placed.map((p) => p.col)) + 1;
  const rows = Math.max(0, ...placed.map((p) => p.row)) + 1;
  const width = cols * (W + GX) + 8;
  const height = rows * (H + GY) + 8;
  return (
    <div className="flow-map">
      <svg width={width} height={height} role="img" aria-label={t("bots.map")}>
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill="currentColor" />
          </marker>
        </defs>
        {placed.flatMap((p) =>
          edges(flow.nodes[p.id]).map((e, i) => {
            const a = pos.get(p.id);
            const b = pos.get(e.target);
            if (!a || !b) return null;
            const x1 = a.x + W;
            const y1 = a.y + H / 2;
            const x2 = b.x;
            const y2 = b.y + H / 2;
            const mx = (x1 + x2) / 2;
            return (
              <g key={`${p.id}-${i}`} className="edge">
                <path d={`M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`} fill="none" stroke="currentColor" markerEnd="url(#arrow)" />
                {(flow.nodes[p.id].type === "condition" || flow.nodes[p.id].type === "buttons") && (
                  <text x={x1 + 4} y={y1 - 3 - i * 11} fontSize="10">{e.label.slice(0, 14)}</text>
                )}
              </g>
            );
          }),
        )}
        {placed.map((p) => {
          const at = pos.get(p.id)!;
          const n = flow.nodes[p.id];
          return (
            <g
              key={p.id}
              className={`map-node ${selected === p.id ? "selected" : ""} ${p.reachable ? "" : "lost"}`}
              transform={`translate(${at.x},${at.y})`}
              onClick={() => onSelect(p.id)}
              tabIndex={0}
              role="button"
              aria-label={`${p.id}: ${t(`bots.node_${n.type}`)}`}
              onKeyDown={(e) => e.key === "Enter" && onSelect(p.id)}
            >
              <rect width={W} height={H} rx="8" />
              <text x="10" y="20" fontSize="12" fontWeight="600">{(flow.start === p.id ? "▶ " : "") + p.id.slice(0, 18)}</text>
              <text x="10" y="40" fontSize="11" className="sub">{(t(`bots.node_${n.type}`) + (summary(n) ? ": " + summary(n) : "")).slice(0, 26)}</text>
            </g>
          );
        })}
      </svg>
      {placed.some((p) => !p.reachable) && <div className="muted small">{t("bots.unreachable")}</div>}
    </div>
  );
}

function NextSelect({ label, value, ids, self, onChange, none }: { label: string; value?: string; ids: string[]; self: string; onChange: (v: string) => void; none: string }) {
  return (
    <label className="field">
      {label}
      <select value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        <option value="">{none}</option>
        {ids.filter((i) => i !== self).map((i) => <option key={i} value={i}>{i}</option>)}
      </select>
    </label>
  );
}

function NodeCard(props: {
  id: string;
  node: BotNode;
  ids: string[];
  isStart: boolean;
  open: boolean;
  problems: string[];
  flows: WhatsAppFlow[];
  onToggle: () => void;
  onChange: (n: BotNode) => void;
  onStart: () => void;
  onRename: (to: string) => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const { id, node: n, ids, onChange } = props;
  const [draftId, setDraftId] = useState(id);
  const set = (patch: Partial<BotNode>) => onChange({ ...n, ...patch });
  const endOfFlow = t("bots.endOfFlow");
  const next = <NextSelect label={t("bots.next")} value={n.next} ids={ids} self={id} none={endOfFlow} onChange={(v) => set({ next: v || undefined })} />;

  return (
    <div className={`node-card ${props.open ? "open" : ""} ${props.problems.length ? "has-problems" : ""}`}>
      <button type="button" className="node-head" onClick={props.onToggle} aria-expanded={props.open}>
        <span className="pill">{t(`bots.node_${n.type}`)}</span>
        <strong>{id}</strong>
        {props.isStart && <span className="pill t-approved">{t("bots.start")}</span>}
        <span className="muted small grow">{summary(n).slice(0, 80)}</span>
        {props.problems.length > 0 && <span className="field-error">!</span>}
      </button>
      {props.open && (
        <div className="node-body form">
          {props.problems.map((p, i) => <div key={i} className="field-error">{p}</div>)}
          <div className="row">
            <label className="field">
              {t("bots.nodeId")}
              <input
                value={draftId}
                maxLength={64}
                onChange={(e) => setDraftId(e.target.value.replace(/[^A-Za-z0-9_-]/g, ""))}
                onBlur={() => {
                  if (draftId && draftId !== id && !ids.includes(draftId)) props.onRename(draftId);
                  else setDraftId(id);
                }}
              />
            </label>
            <div className="actions">
              {!props.isStart && <button type="button" className="link" onClick={props.onStart}>{t("bots.makeStart")}</button>}
              <button type="button" className="link" onClick={props.onDelete}>{t("bots.deleteNode")}</button>
            </div>
          </div>

          {(n.type === "message" || n.type === "buttons" || n.type === "question" || n.type === "flow" || n.type === "handoff" || n.type === "end") && (
            <label className="field">
              {t("bots.text")}
              <textarea rows={3} maxLength={n.type === "buttons" || n.type === "flow" ? MAX_BODY : 4096} value={n.text ?? ""} onChange={(e) => set({ text: e.target.value })} />
            </label>
          )}

          {n.type === "message" && next}

          {n.type === "buttons" && (
            <fieldset className="field">
              <legend>{t("bots.buttons")}</legend>
              {(n.buttons ?? []).map((b, i) => (
                <div className="button-row" key={i}>
                  <input
                    aria-label={t("bots.buttonTitle")}
                    maxLength={MAX_BUTTON_TITLE}
                    placeholder={t("bots.buttonTitle")}
                    value={b.title}
                    onChange={(e) => set({ buttons: n.buttons!.map((x, j) => (j === i ? { ...x, title: e.target.value } : x)) })}
                  />
                  <input
                    aria-label={t("bots.buttonId")}
                    placeholder={t("bots.buttonId")}
                    value={b.id}
                    onChange={(e) => set({ buttons: n.buttons!.map((x, j) => (j === i ? { ...x, id: e.target.value } : x)) })}
                  />
                  <select
                    aria-label={t("bots.next")}
                    value={b.next ?? ""}
                    onChange={(e) => set({ buttons: n.buttons!.map((x, j) => (j === i ? { ...x, next: e.target.value || undefined } : x)) })}
                  >
                    <option value="">{endOfFlow}</option>
                    {ids.filter((x) => x !== id).map((x) => <option key={x} value={x}>{x}</option>)}
                  </select>
                  <button type="button" className="link" onClick={() => set({ buttons: n.buttons!.filter((_, j) => j !== i) })}>{t("bots.remove")}</button>
                </div>
              ))}
              {(n.buttons ?? []).length < MAX_BUTTONS && (
                <button
                  type="button"
                  className="link"
                  onClick={() => set({ buttons: [...(n.buttons ?? []), { id: `option_${(n.buttons ?? []).length + 1}`, title: "" }] })}
                >
                  {t("bots.addButton")}
                </button>
              )}
            </fieldset>
          )}

          {n.type === "question" && (
            <>
              <div className="row">
                <label className="field">
                  {t("bots.saveAnswerAs")}
                  <input value={n.var ?? ""} onChange={(e) => set({ var: e.target.value.replace(/[^A-Za-z0-9_]/g, "") })} />
                </label>
                <label className="field">
                  {t("bots.answerKind")}
                  <select value={n.kind ?? "text"} onChange={(e) => set({ kind: e.target.value as BotNode["kind"] })}>
                    {(["text", "number", "email", "phone"] as const).map((k) => <option key={k} value={k}>{t(`bots.kind_${k}`)}</option>)}
                  </select>
                </label>
              </div>
              {next}
            </>
          )}

          {n.type === "condition" && (
            <>
              <div className="row">
                <label className="field">
                  {t("bots.checkAnswer")}
                  <input value={n.var ?? ""} onChange={(e) => set({ var: e.target.value.replace(/[^A-Za-z0-9_]/g, "") })} />
                </label>
                <label className="field">
                  {t("bots.operator")}
                  <select value={n.op ?? "equals"} onChange={(e) => set({ op: e.target.value as BotNode["op"] })}>
                    {(["equals", "not_equals", "contains", "exists", "gt", "lt"] as const).map((o) => <option key={o} value={o}>{t(`bots.op_${o}`)}</option>)}
                  </select>
                </label>
              </div>
              {n.op !== "exists" && (
                <label className="field">
                  {t("bots.value")}
                  <input value={n.value ?? ""} onChange={(e) => set({ value: e.target.value })} />
                </label>
              )}
              <div className="row">
                <NextSelect label={t("bots.then")} value={n.then} ids={ids} self={id} none={t("bots.choose")} onChange={(v) => set({ then: v })} />
                <NextSelect label={t("bots.else")} value={n.else} ids={ids} self={id} none={endOfFlow} onChange={(v) => set({ else: v || undefined })} />
              </div>
            </>
          )}

          {n.type === "set" && (
            <>
              <div className="row">
                <label className="field">
                  {t("bots.saveAnswerAs")}
                  <input value={n.var ?? ""} onChange={(e) => set({ var: e.target.value.replace(/[^A-Za-z0-9_]/g, "") })} />
                </label>
                <label className="field">
                  {t("bots.value")}
                  <input value={n.value ?? ""} onChange={(e) => set({ value: e.target.value })} />
                </label>
              </div>
              {next}
            </>
          )}

          {n.type === "tag" && (
            <>
              <label className="field">
                {t("bots.tag")}
                <input value={n.tag ?? ""} maxLength={50} onChange={(e) => set({ tag: e.target.value })} />
              </label>
              {next}
            </>
          )}

          {n.type === "template" && (
            <>
              <div className="row">
                <label className="field">
                  {t("bots.templateName")}
                  <input value={n.template?.name ?? ""} onChange={(e) => set({ template: { ...n.template!, name: e.target.value } })} />
                </label>
                <label className="field">
                  {t("bots.templateLanguage")}
                  <input value={n.template?.language ?? ""} onChange={(e) => set({ template: { ...n.template!, language: e.target.value } })} />
                </label>
              </div>
              <label className="field">
                {t("bots.templateParams")}
                <input
                  placeholder={t("bots.templateParamsPlaceholder")}
                  value={(n.template?.params ?? []).join(", ")}
                  onChange={(e) => set({ template: { ...n.template!, params: e.target.value ? e.target.value.split(",").map((x) => x.trim()) : [] } })}
                />
              </label>
              {next}
            </>
          )}

          {n.type === "flow" && (
            <>
              <div className="row">
                <label className="field">
                  {t("bots.flow")}
                  <select value={n.flow_id ?? ""} onChange={(e) => set({ flow_id: e.target.value })}>
                    <option value="">{t("bots.choose")}</option>
                    {props.flows.map((f) => <option key={f.id} value={f.id}>{f.name} ({t(`flows.status_${f.status}`)})</option>)}
                  </select>
                </label>
                <label className="field">
                  {t("bots.flowButton")}
                  <input value={n.cta ?? ""} maxLength={MAX_BUTTON_TITLE} onChange={(e) => set({ cta: e.target.value })} />
                </label>
              </div>
              <div className="row">
                <label className="field">
                  {t("bots.flowAnswersPrefix")}
                  <input value={n.var ?? ""} onChange={(e) => set({ var: e.target.value.replace(/[^A-Za-z0-9_]/g, "") || undefined })} />
                  <span className="muted small">{t("bots.flowAnswersHelp")}</span>
                </label>
                <label className="field">
                  {t("bots.flowScreen")}
                  <input value={n.screen ?? ""} onChange={(e) => set({ screen: e.target.value || undefined })} />
                </label>
              </div>
              {next}
            </>
          )}

          {n.type === "handoff" && (
            <label className="field">
              {t("bots.handoffReason")}
              <input value={n.reason ?? ""} onChange={(e) => set({ reason: e.target.value })} />
              <span className="muted small">{t("bots.handoffHelp")}</span>
            </label>
          )}
        </div>
      )}
    </div>
  );
}

function Sessions({ botId }: { botId: string }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["bot-sessions", botId],
    queryFn: async () => (await api<{ data: BotSession[] }>("GET", `/v1/bots/${botId}/sessions`)).data,
  });
  const rows = q.data ?? [];
  return (
    <>
      <h3>{t("bots.sessions")}</h3>
      {rows.length === 0 ? (
        <div className="muted">{t("bots.noSessions")}</div>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr><th>{t("bots.started")}</th><th>{t("bots.status")}</th><th>{t("bots.endReason")}</th><th>{t("bots.answers")}</th></tr>
            </thead>
            <tbody>
              {rows.map((s) => (
                <tr key={s.id}>
                  <td className="small">{new Date(s.started_at).toLocaleString()}</td>
                  <td>{t(`bots.session_${s.status}`)}</td>
                  <td className="small">{s.end_reason ?? "—"}</td>
                  <td className="small">{Object.entries(s.variables ?? {}).map(([k, v]) => `${k}: ${v}`).join(", ") || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
