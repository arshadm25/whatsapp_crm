import { Fragment, useEffect, useMemo, useRef, useState, type ChangeEvent, type FormEvent, type KeyboardEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useInboxCounts, useMe, useMembers, usePhoneNumbers, useQuickReplies } from "../api/hooks";
import type { ConsentEvent, Contact, Conversation, InboxCounts, Media, Message, Note, Page } from "../api/types";
import MediaPreview from "../components/MediaPreview";
import TemplateComposer from "../components/TemplateComposer";
import { ACCEPT, MEDIA_TYPES, formatSize, mediaKind, takesCaption } from "../lib/media";
import { captionOf, messageText } from "../lib/messages";
import Icon from "../components/Icon";

type Filter = "all" | "unread" | "mine" | "unassigned" | "closed";

const FILTER_COUNT: Record<Filter, keyof InboxCounts> = {
  all: "open",
  unread: "unread_conversations",
  mine: "mine",
  unassigned: "unassigned",
  closed: "closed",
};

// The API has no unread filter, so Unread loads open conversations and keeps the unread ones.
const FILTER_QUERY: Record<Filter, string> = {
  all: "status=open",
  unread: "status=open",
  mine: "assignee_id=me&status=open",
  unassigned: "assignee_id=none&status=open",
  closed: "status=closed",
};

function contactName(c: Conversation["contact"]) {
  return c.name ?? c.profile_name ?? `+${c.wa_id}`;
}

function initials(name: string) {
  const parts = name.replace(/[^\p{L}\p{N} ]/gu, "").trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "#";
  return (parts[0][0] + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase();
}

// A steady colour per contact, so the same person keeps the same avatar.
function avatarTone(key: string) {
  let h = 2166136261;
  for (const ch of key) h = Math.imul(h ^ ch.charCodeAt(0), 16777619) >>> 0;
  return `av-c${h % 6}`;
}

function Avatar({ contact, large }: { contact: Conversation["contact"]; large?: boolean }) {
  return <span className={`av ${avatarTone(contact.id)}${large ? " lg" : ""}`}>{initials(contactName(contact))}</span>;
}

function clock(d: Date) {
  return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// Whole days between the date and today, by the calendar rather than by 24-hour blocks.
function daysAgo(d: Date) {
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  return Math.round((day(new Date()) - day(d)) / 86400000);
}

type T = (key: string) => string;

// Chat list time, as WhatsApp shows it: a clock time today, then Yesterday, a weekday, a date.
function listTime(iso: string | null, t: T) {
  if (!iso) return "";
  const d = new Date(iso);
  const ago = daysAgo(d);
  if (ago <= 0) return clock(d);
  if (ago === 1) return t("inbox.yesterday");
  if (ago < 7) return d.toLocaleDateString([], { weekday: "long" });
  return d.toLocaleDateString();
}

// Label on the separator above each day's messages.
function dayLabel(iso: string, t: T) {
  const d = new Date(iso);
  const ago = daysAgo(d);
  if (ago <= 0) return t("inbox.today");
  if (ago === 1) return t("inbox.yesterday");
  if (ago < 7) return d.toLocaleDateString([], { weekday: "long" });
  return d.toLocaleDateString([], { day: "numeric", month: "long", year: "numeric" });
}

function shortTime(iso: string | null) {
  if (!iso) return "";
  const d = new Date(iso);
  return daysAgo(d) <= 0 ? clock(d) : d.toLocaleDateString();
}

// "5h 20m" until the customer service window closes.
function timeLeft(iso: string | null) {
  const mins = iso ? Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 60000)) : 0;
  return mins >= 60 ? `${Math.floor(mins / 60)}h ${mins % 60}m` : `${mins}m`;
}

export default function Inbox() {
  const { t } = useTranslation();
  const { id } = useParams();
  const navigate = useNavigate();
  const [filter, setFilter] = useState<Filter>("all");
  const [search, setSearch] = useState("");
  const counts = useInboxCounts().data;

  const list = useQuery({
    queryKey: ["conversations", FILTER_QUERY[filter], search],
    queryFn: () =>
      api<Page<Conversation>>(
        "GET",
        `/v1/conversations?limit=100&${FILTER_QUERY[filter]}${search ? `&q=${encodeURIComponent(search)}` : ""}`,
      ),
  });
  const rows = filter === "unread" ? list.data?.data.filter((c) => c.unread_count > 0) : list.data?.data;

  return (
    <section className={`inbox ${id ? "has-thread" : ""}`}>
      <aside className="inbox-list card">
        <h1>{t("inbox.title")}</h1>
        <label className="search-box">
          <Icon name="search" size="s" />
          <input className="search" placeholder={t("inbox.search")} value={search} onChange={(e) => setSearch(e.target.value)} />
        </label>
        <div className="inbox-filters" role="tablist">
          {(Object.keys(FILTER_QUERY) as Filter[]).map((f) => (
            <button key={f} role="tab" aria-selected={filter === f} className={filter === f ? "active" : ""} onClick={() => setFilter(f)}>
              {t(`inbox.filter_${f}`)}
              {counts && counts[FILTER_COUNT[f]] > 0 && <span className="cnt">{counts[FILTER_COUNT[f]]}</span>}
            </button>
          ))}
        </div>
        {list.isLoading && <div className="muted">{t("common.loading")}</div>}
        {rows?.length === 0 && <div className="muted small">{t("inbox.empty")}</div>}
        <ul>
          {rows?.map((c) => (
            <li key={c.id}>
              <button className={`conv ${c.id === id ? "selected" : ""} ${c.unread_count > 0 ? "unread" : ""}`} onClick={() => navigate(`/inbox/${c.id}`)}>
                <Avatar contact={c.contact} />
                <span className="conv-main">
                  <span className="conv-top">
                    <strong>{contactName(c.contact)}</strong>
                    <span className="conv-time">{listTime(c.last_message_at, t)}</span>
                  </span>
                  <span className="conv-bottom">
                    <span className="preview">{c.last_message_preview}</span>
                    {c.status !== "closed" && c.window.open && (
                      <span className="conv-window" title={t("inbox.windowOpen", { left: timeLeft(c.window.expires_at) })}>
                        <Icon name="clock" size="xs" />
                        {timeLeft(c.window.expires_at)}
                      </span>
                    )}
                    {c.unread_count > 0 && <span className="badge">{c.unread_count}</span>}
                  </span>
                  {c.contact.tags.length > 0 && (
                    <span className="conv-tags">
                      {c.contact.tags.slice(0, 2).map((tag) => <span key={tag} className="chip">{tag}</span>)}
                    </span>
                  )}
                </span>
              </button>
            </li>
          ))}
        </ul>
      </aside>
      {id ? (
        <Thread key={id} id={id} />
      ) : (
        <div className="inbox-empty card">
          <span className="empty-mark"><Icon name="message" size="lg" /></span>
          <strong>{t("inbox.pick")}</strong>
          <span className="muted small">{t("inbox.pickHint")}</span>
        </div>
      )}
    </section>
  );
}

function Thread({ id }: { id: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const members = useMembers();
  const numbers = usePhoneNumbers();
  const [showNotes, setShowNotes] = useState(() => window.matchMedia?.("(min-width: 1200px)").matches ?? false);
  const [error, setError] = useState("");
  const bottom = useRef<HTMLDivElement>(null);

  const conv = useQuery({
    queryKey: ["conversation", id],
    queryFn: () => api<Conversation>("GET", `/v1/conversations/${id}`),
  });
  const notes = useNotes(id);
  const contactId = conv.data?.contact.id;
  const contact = useQuery({
    queryKey: ["contact", contactId],
    queryFn: () => api<Contact>("GET", `/v1/contacts/${contactId}`),
    enabled: !!contactId,
  });
  const msgs = useInfiniteQuery({
    queryKey: ["conversation-messages", id],
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      api<Page<Message>>("GET", `/v1/conversations/${id}/messages?limit=50${pageParam ? `&cursor=${pageParam}` : ""}`),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const messages = useMemo(() => (msgs.data?.pages.flatMap((p) => p.data) ?? []).slice().reverse(), [msgs.data]);
  const newest = messages[messages.length - 1]?.id;
  // Team notes sit in the timeline between the messages written around the same time.
  const timeline = useMemo(() => {
    const oldest = messages[0]?.created_at ?? "";
    const items: ({ kind: "message"; at: string; m: Message } | { kind: "note"; at: string; n: Note })[] = [
      ...messages.map((m) => ({ kind: "message" as const, at: m.created_at, m })),
      ...(notes.data ?? []).filter((n) => !msgs.hasNextPage || n.created_at >= oldest).map((n) => ({ kind: "note" as const, at: n.created_at, n })),
    ];
    return items.sort((a, b) => a.at.localeCompare(b.at));
  }, [messages, notes.data, msgs.hasNextPage]);

  // A block body, not an expression: newer Chrome returns a Promise from scrollIntoView, and
  // React would call that Promise as the effect's cleanup and crash the page.
  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [newest]);

  // Notes, quick reply chips and the growing reply box arrive after the first scroll and would
  // push the newest message out of view, so stay at the bottom while the reader is there.
  const pane = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const loaded = !!conv.data;
  useEffect(() => {
    const el = pane.current;
    if (!el) return;
    const stick = () => {
      if (atBottom.current) el.scrollTop = el.scrollHeight;
    };
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(stick);
    ro?.observe(el);
    const mo = new MutationObserver(stick);
    mo.observe(el, { childList: true, subtree: true });
    return () => {
      ro?.disconnect();
      mo.disconnect();
    };
  }, [loaded]);

  // Opening a conversation with unread messages shows blue ticks to the customer.
  const c = conv.data;
  useEffect(() => {
    if (!c || c.unread_count === 0) return;
    // Meta refuses read receipts for unsupported messages, and marking a later message read
    // marks the earlier ones too, so prefer the newest message Meta understands.
    const inbound = [...messages].reverse().filter((m) => m.direction === "inbound" && m.wamid);
    const lastIn = inbound.find((m) => m.type !== "unsupported") ?? inbound[0];
    if (lastIn) api("POST", `/v1/messages/${lastIn.id}/read`).catch(() => undefined);
  }, [c, messages]);

  if (!c) return <div className="inbox-thread card muted">{t("common.loading")}</div>;
  const number = numbers.data?.find((n) => n.id === c.phone_number_id);

  const patch = async (body: Record<string, unknown>) => {
    setError("");
    try {
      await api("PATCH", `/v1/conversations/${id}`, body);
      qc.invalidateQueries({ queryKey: ["conversation", id] });
      qc.invalidateQueries({ queryKey: ["conversations"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["conversation-messages", id] });
    qc.invalidateQueries({ queryKey: ["conversation", id] });
  };

  return (
    <div className="inbox-thread card">
      <header className="thread-head">
        <Link className="back link" to="/inbox">←</Link>
        <Avatar contact={c.contact} />
        <div>
          <strong>{contactName(c.contact)}</strong>
          <div className="muted small">
            +{c.contact.wa_id}
            {number && ` · ${t("inbox.via", { number: number.display_phone_number })}`}
            {contact.data?.conversation_count !== undefined && ` · ${t("inbox.conversationCount", { count: contact.data.conversation_count })}`}
          </div>
        </div>
        <span className={`pill window-pill ${c.window.open ? "ok" : ""}`}>
          {c.window.open
            ? t("inbox.windowOpen", { left: timeLeft(c.window.expires_at) })
            : t("inbox.windowClosed")}
        </span>
        <select value={c.assignee_id ?? ""} onChange={(e) => patch({ assignee_id: e.target.value || null })} aria-label={t("inbox.assignee")}>
          <option value="">{t("inbox.unassigned")}</option>
          {members.data?.map((m) => (
            <option key={m.id} value={m.id}>{m.name}</option>
          ))}
        </select>
        <button className={c.status === "closed" ? "" : "ghost"} onClick={() => patch({ status: c.status === "closed" ? "open" : "closed" })}>
          <Icon name={c.status === "closed" ? "refresh" : "check"} size="xs" />
          {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
        </button>
        <button className={showNotes ? "active" : ""} onClick={() => setShowNotes(!showNotes)}>{t("inbox.details")}</button>
      </header>
      {error && <div className="error">{error}</div>}
      <div className="thread-body">
        <div
          className="messages"
          ref={pane}
          onScroll={(e) => {
            const el = e.currentTarget;
            atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
          }}
        >
          {msgs.hasNextPage && (
            <button className="link older" onClick={() => msgs.fetchNextPage()}>{t("inbox.older")}</button>
          )}
          {timeline.map((item, i) => {
            const prev = timeline[i - 1];
            const newDay = !prev || new Date(prev.at).toDateString() !== new Date(item.at).toDateString();
            // The first bubble of a run from one side gets the tail, as in WhatsApp.
            const first = newDay || prev.kind !== "message" || item.kind !== "message" || prev.m.direction !== item.m.direction;
            return (
              <Fragment key={item.kind === "note" ? `note-${item.n.id}` : item.m.id}>
                {newDay && <div className="day-sep"><span>{dayLabel(item.at, t)}</span></div>}
                {item.kind === "note" ? (
                  <div className="inline-note">
                    {t("inbox.noteBy", { name: item.n.author_name })}: {item.n.body}
                  </div>
                ) : (
                  <MessageBubble m={item.m} first={first} />
                )}
              </Fragment>
            );
          })}
          <div ref={bottom} />
        </div>
        {showNotes && <ContactPanel conv={c} notes={notes.data ?? []} />}
      </div>
      <footer className="composer">
        {c.window.open ? (
          <TextComposer conv={c} onSent={refresh} />
        ) : (
          <>
            <div className="closed-hint"><Icon name="lock" size="xs" />{t("inbox.closedHint")}</div>
            <TemplateComposer phoneNumberId={c.phone_number_id} accountId={number?.whatsapp_account_id} to={c.contact.wa_id} onSent={refresh} />
          </>
        )}
      </footer>
    </div>
  );
}

// WhatsApp's single and double ticks; read turns them blue.
function Ticks({ status }: { status: Message["status"] }) {
  if (status === "queued") return <Icon name="clock" size="xs" />;
  if (status === "failed") return <Icon name="alert" size="xs" />;
  if (status !== "sent" && status !== "delivered" && status !== "read") return null;
  return (
    <svg className="ticks-svg" viewBox="0 0 18 12" aria-hidden="true">
      {status === "sent" ? <path d="M3 6.5 6.5 10 14 2.5" /> : <><path d="M1 6.5 4.5 10 12 2.5" /><path d="M7.5 9.2 8.3 10 15.8 2.5" /></>}
    </svg>
  );
}

function MessageBubble({ m, first }: { m: Message; first: boolean }) {
  const { t } = useTranslation();
  const media = MEDIA_TYPES.has(m.type);
  const text = media ? captionOf(m) : messageText(m);
  const meta = (
    <span className={`bubble-meta ${text ? "" : "static"}`}>
      {clock(new Date(m.created_at))}
      {m.direction === "outbound" && (
        <span className={`tick tick-${m.status}`} title={t(`send.status_${m.status}`)} aria-label={t(`send.status_${m.status}`)}>
          <Ticks status={m.status} />
        </span>
      )}
    </span>
  );
  return (
    <div className={`bubble ${m.direction}${first ? " first" : ""}${media ? " has-media" : ""}`}>
      {m.type === "template" && (
        <div className="bubble-tag">{t("inbox.templateTag")}</div>
      )}
      {m.origin === "phone_app" && <div className="bubble-tag phone">{t("inbox.fromPhone")}</div>}
      {media && <MediaPreview message={m} />}
      {text ? (
        <div className="bubble-text">
          {text}
          <span className={`meta-space ${m.direction}`} />
          {meta}
        </div>
      ) : (
        meta
      )}
      {m.error && <div className="bubble-error">{m.error.message}</div>}
    </div>
  );
}

function TextComposer({ conv, onSent }: { conv: Conversation; onSent: () => void }) {
  const { t } = useTranslation();
  const replies = useQuickReplies();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // Typing "/shortcut" offers the matching quick replies.
  const slash = text.startsWith("/") && !text.includes(" ") ? text.slice(1).toLowerCase() : null;
  const matches = slash === null ? [] : (replies.data ?? []).filter((r) => r.shortcut.toLowerCase().startsWith(slash));

  const [file, setFile] = useState<File | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);

  // Grow the box with its text, up to about six lines, like WhatsApp's.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 140)}px`;
  }, [text]);
  const kind = file ? mediaKind(file.type) : null;

  const pick = (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0] ?? null;
    e.target.value = "";
    if (!f) return;
    const k = mediaKind(f.type);
    if (!k) return setError(t("inbox.fileType"));
    if (f.size > k.limit) return setError(t("inbox.fileTooLarge", { name: f.name, limit: formatSize(k.limit) }));
    setError("");
    setFile(f);
  };

  const post = (body: Record<string, unknown>) =>
    api("POST", "/v1/messages", { phone_number_id: conv.phone_number_id, to: conv.contact.wa_id, ...body });

  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!text.trim() && !file) return;
    setBusy(true);
    setError("");
    try {
      if (file && kind) {
        const form = new FormData();
        form.append("phone_number_id", conv.phone_number_id);
        form.append("file", file);
        const media = await api<Media>("POST", "/v1/media", form);
        const caption = takesCaption(kind.kind) ? text.trim() : "";
        const part: Record<string, unknown> = { media_id: media.id };
        if (caption) part.caption = caption;
        if (kind.kind === "document") part.filename = file.name;
        await post({ type: kind.kind, [kind.kind]: part });
        // Audio and stickers cannot carry a caption, so any text goes as its own message.
        if (!caption && text.trim()) await post({ type: "text", text: { body: text } });
        setFile(null);
      } else {
        await post({ type: "text", text: { body: text } });
      }
      setText("");
      onSent();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    } finally {
      setBusy(false);
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      if (matches.length) setText(matches[0].body);
      else send();
    }
  };

  return (
    <form className="text-composer" onSubmit={send}>
      {matches.length > 0 && (
        <ul className="quick-replies">
          {matches.map((r) => (
            <li key={r.id}>
              <button type="button" className="link" onClick={() => setText(r.body)}>
                /{r.shortcut} <span className="muted">{r.body}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {matches.length === 0 && !text && (replies.data?.length ?? 0) > 0 && (
        <div className="reply-chips">
          {replies.data!.slice(0, 6).map((r) => (
            <button type="button" key={r.id} className="chip selectable" title={r.body} onClick={() => setText(r.body)}>/{r.shortcut}</button>
          ))}
        </div>
      )}
      {error && <div className="error">{error}</div>}
      {file && (
        <div className="attachment">
          📎 {file.name} <span className="muted">· {formatSize(file.size)}</span>
          <button type="button" className="link" onClick={() => setFile(null)}>{t("inbox.removeFile")}</button>
        </div>
      )}
      <button type="button" className="attach" title={t("inbox.attach")} aria-label={t("inbox.attach")} onClick={() => fileInput.current?.click()}>
        <Icon name="plus" />
      </button>
      <input ref={fileInput} type="file" accept={ACCEPT} hidden onChange={pick} />
      <textarea
        ref={box}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        rows={1}
        maxLength={file && kind && takesCaption(kind.kind) ? 1024 : 4096}
        placeholder={file ? t("inbox.captionPlaceholder") : t("inbox.typeMessage")}
        title={t("inbox.placeholder")}
        aria-label={t("inbox.typeMessage")}
      />
      <button className="primary send" disabled={busy || (!text.trim() && !file)} title={t("send.send")} aria-label={t("send.send")}>
        <Icon name="send" />
      </button>
    </form>
  );
}

function useNotes(id: string) {
  return useQuery({
    queryKey: ["notes", id],
    queryFn: async () => (await api<{ data: Note[] }>("GET", `/internal/inbox/conversations/${id}/notes`)).data,
  });
}

function ContactPanel({ conv, notes }: { conv: Conversation; notes: Note[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const role = useMe().data?.tenant?.role;
  const replies = useQuickReplies();
  const id = conv.id;
  const ct = conv.contact;
  const consent = useQuery({
    queryKey: ["consent", ct.id],
    queryFn: async () => (await api<{ data: ConsentEvent[] }>("GET", `/internal/contacts/${ct.id}/consent`)).data,
  });
  const lastOptIn = consent.data?.find((e) => e.kind === "opt_in");
  const [body, setBody] = useState("");
  const [shortcut, setShortcut] = useState("");
  const [replyBody, setReplyBody] = useState("");
  const [error, setError] = useState("");

  const addNote = async (e: FormEvent) => {
    e.preventDefault();
    await api("POST", `/internal/inbox/conversations/${id}/notes`, { body });
    setBody("");
    qc.invalidateQueries({ queryKey: ["notes", id] });
  };
  const addReply = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("POST", "/internal/inbox/quick-replies", { shortcut, body: replyBody });
      setShortcut("");
      setReplyBody("");
      qc.invalidateQueries({ queryKey: ["quick-replies"] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("common.error"));
    }
  };
  const optPill = ct.blocked ? "q-red" : ct.opt_in_status === "opted_in" ? "q-green" : ct.opt_in_status === "opted_out" ? "q-red" : "";

  return (
    <aside className="notes">
      <div className="contact-card">
        <Avatar contact={ct} large />
        <strong>{contactName(ct)}</strong>
        <span className="muted small">+{ct.wa_id}</span>
        <span className={`pill ${optPill}`}>{ct.blocked ? t("contacts.blocked") : t(`contacts.consent_${ct.opt_in_status}`)}</span>
      </div>
      <dl className="kv">
        {lastOptIn && <><dt>{t("inbox.optInSource")}</dt><dd>{t(`contacts.source_${lastOptIn.source}`, { defaultValue: lastOptIn.source })}</dd></>}
        {ct.opted_in_at && <><dt>{t("inbox.optedIn")}</dt><dd>{new Date(ct.opted_in_at).toLocaleDateString()}</dd></>}
        {ct.language && <><dt>{t("contacts.language")}</dt><dd>{ct.language}</dd></>}
        <dt>{t("inbox.firstSeen")}</dt><dd>{new Date(ct.created_at).toLocaleDateString()}</dd>
      </dl>
      {ct.tags.length > 0 && (
        <>
          <h3>{t("contacts.tags")}</h3>
          <div>{ct.tags.map((tag) => <span key={tag} className="chip">{tag}</span>)}</div>
        </>
      )}
      <h3>{t("inbox.notes")}</h3>
      <p className="muted small">{t("inbox.notesHint")}</p>
      {notes.map((n) => (
        <div key={n.id} className="note">
          <div>{n.body}</div>
          <div className="muted small">{n.author_name} · {shortTime(n.created_at)}</div>
        </div>
      ))}
      <form className="form" onSubmit={addNote}>
        <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={2} required />
        <button>{t("inbox.addNote")}</button>
      </form>
      {(role === "owner" || role === "admin") && (
        <>
          <h3>{t("inbox.quickReplies")}</h3>
          {replies.data?.map((r) => (
            <div key={r.id} className="small">
              <strong>/{r.shortcut}</strong> {r.body}
            </div>
          ))}
          <form className="form" onSubmit={addReply}>
            <input value={shortcut} onChange={(e) => setShortcut(e.target.value)} placeholder="/hours" required />
            <textarea value={replyBody} onChange={(e) => setReplyBody(e.target.value)} rows={2} required />
            {error && <div className="error">{error}</div>}
            <button>{t("inbox.addQuickReply")}</button>
          </form>
        </>
      )}
    </aside>
  );
}
