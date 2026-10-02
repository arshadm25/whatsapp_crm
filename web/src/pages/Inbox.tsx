import { useEffect, useMemo, useRef, useState, type ChangeEvent, type FormEvent, type KeyboardEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, useMembers, usePhoneNumbers, useQuickReplies } from "../api/hooks";
import type { ConsentEvent, Conversation, Media, Message, Note, Page } from "../api/types";
import MediaPreview from "../components/MediaPreview";
import TemplateComposer from "../components/TemplateComposer";
import { ACCEPT, MEDIA_TYPES, formatSize, mediaKind, takesCaption } from "../lib/media";
import { captionOf, messageText, statusTick } from "../lib/messages";
import Icon from "../components/Icon";

type Filter = "all" | "mine" | "unassigned" | "closed";

const FILTER_QUERY: Record<Filter, string> = {
  all: "status=open",
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

function shortTime(iso: string | null) {
  if (!iso) return "";
  const d = new Date(iso);
  const sameDay = d.toDateString() === new Date().toDateString();
  return sameDay ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : d.toLocaleDateString();
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

  const list = useQuery({
    queryKey: ["conversations", filter, search],
    queryFn: () =>
      api<Page<Conversation>>(
        "GET",
        `/v1/conversations?limit=100&${FILTER_QUERY[filter]}${search ? `&q=${encodeURIComponent(search)}` : ""}`,
      ),
  });

  return (
    <section className={`inbox ${id ? "has-thread" : ""}`}>
      <aside className="inbox-list card">
        <h1>{t("inbox.title")}</h1>
        <input className="search" placeholder={t("inbox.search")} value={search} onChange={(e) => setSearch(e.target.value)} />
        <div className="tabs">
          {(Object.keys(FILTER_QUERY) as Filter[]).map((f) => (
            <button key={f} className={filter === f ? "active" : ""} onClick={() => setFilter(f)}>
              {t(`inbox.filter_${f}`)}
            </button>
          ))}
        </div>
        {list.isLoading && <div className="muted">{t("common.loading")}</div>}
        {list.data?.data.length === 0 && <div className="muted small">{t("inbox.empty")}</div>}
        <ul>
          {list.data?.data.map((c) => (
            <li key={c.id}>
              <button className={`conv ${c.id === id ? "selected" : ""}`} onClick={() => navigate(`/inbox/${c.id}`)}>
                <span className="av">{initials(contactName(c.contact))}</span>
                <span className="conv-main">
                  <span className="conv-top">
                    <strong>{contactName(c.contact)}</strong>
                    <span className="muted small">{shortTime(c.last_message_at)}</span>
                  </span>
                  <span className="conv-bottom">
                    <span className="muted small preview">{c.last_message_preview}</span>
                    {c.unread_count > 0 && <span className="badge">{c.unread_count}</span>}
                  </span>
                  {(c.contact.tags.length > 0 || c.status !== "closed") && (
                    <span className="conv-tags">
                      {c.contact.tags.slice(0, 2).map((tag) => <span key={tag} className="chip">{tag}</span>)}
                      {c.status !== "closed" && (
                        <span className="chip muted">{c.window.open ? t("inbox.leftShort", { left: timeLeft(c.window.expires_at) }) : t("inbox.windowClosed")}</span>
                      )}
                    </span>
                  )}
                </span>
              </button>
            </li>
          ))}
        </ul>
      </aside>
      {id ? <Thread key={id} id={id} /> : <div className="inbox-empty card muted">{t("inbox.pick")}</div>}
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

  useEffect(() => bottom.current?.scrollIntoView({ block: "end" }), [newest]);

  // Opening a conversation with unread messages shows blue ticks to the customer.
  const c = conv.data;
  useEffect(() => {
    if (!c || c.unread_count === 0) return;
    const lastIn = [...messages].reverse().find((m) => m.direction === "inbound" && m.wamid);
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
        <span className="av">{initials(contactName(c.contact))}</span>
        <div>
          <strong>{contactName(c.contact)}</strong>
          <div className="muted small">
            +{c.contact.wa_id}
            {number && ` · ${t("inbox.via", { number: number.display_phone_number })}`}
          </div>
        </div>
        <span className={`pill ${c.window.open ? "ok" : ""}`}>
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
        <div className="messages">
          {msgs.hasNextPage && (
            <button className="link older" onClick={() => msgs.fetchNextPage()}>{t("inbox.older")}</button>
          )}
          {timeline.map((item) => item.kind === "note" ? (
            <div key={`note-${item.n.id}`} className="inline-note">
              {t("inbox.noteBy", { name: item.n.author_name })}: {item.n.body}
            </div>
          ) : (
            <MessageBubble key={item.m.id} m={item.m} />
          ))}
          <div ref={bottom} />
        </div>
        {showNotes && <ContactPanel conv={c} notes={notes.data ?? []} />}
      </div>
      <footer className="composer">
        {c.window.open ? (
          <TextComposer conv={c} onSent={refresh} />
        ) : (
          <>
            <div className="muted small">{t("inbox.closedHint")}</div>
            <TemplateComposer phoneNumberId={c.phone_number_id} accountId={number?.whatsapp_account_id} to={c.contact.wa_id} onSent={refresh} />
          </>
        )}
      </footer>
    </div>
  );
}

function MessageBubble({ m }: { m: Message }) {
  const { t } = useTranslation();
  return (
    <div className={`bubble ${m.direction}`}>
      {m.type === "template" && (
        <div className="bubble-tag">{t("inbox.templateTag")}</div>
      )}
      {MEDIA_TYPES.has(m.type) ? (
        <>
          <MediaPreview message={m} />
          {captionOf(m) && <div className="bubble-text">{captionOf(m)}</div>}
        </>
      ) : (
        <div className="bubble-text">{messageText(m)}</div>
      )}
      <div className="bubble-meta">
        {shortTime(m.created_at)}
        {m.direction === "outbound" && (
          <span className={`tick tick-${m.status}`} title={t(`send.status_${m.status}`)}>{statusTick(m.status)}</span>
        )}
        {m.origin === "phone_app" && <span>· {t("inbox.fromPhone")}</span>}
      </div>
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
        📎
      </button>
      <input ref={fileInput} type="file" accept={ACCEPT} hidden onChange={pick} />
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        rows={2}
        maxLength={file && kind && takesCaption(kind.kind) ? 1024 : 4096}
        placeholder={file ? t("inbox.captionPlaceholder") : t("inbox.placeholder")}
      />
      <button className="primary" disabled={busy || (!text.trim() && !file)}>{t("send.send")}</button>
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
        <span className="av lg">{initials(contactName(ct))}</span>
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
