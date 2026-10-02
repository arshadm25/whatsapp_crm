import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api/client";
import { useMe, useMembers, usePhoneNumbers, useQuickReplies } from "../api/hooks";
import type { Conversation, Message, Note, Page } from "../api/types";
import TemplateComposer from "../components/TemplateComposer";
import { messageText, statusTick } from "../lib/messages";

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
                <span className="conv-top">
                  <strong>{contactName(c.contact)}</strong>
                  <span className="muted small">{shortTime(c.last_message_at)}</span>
                </span>
                <span className="conv-bottom">
                  <span className="muted small preview">{c.last_message_preview}</span>
                  {c.unread_count > 0 && <span className="badge">{c.unread_count}</span>}
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
  const [showNotes, setShowNotes] = useState(false);
  const [error, setError] = useState("");
  const bottom = useRef<HTMLDivElement>(null);

  const conv = useQuery({
    queryKey: ["conversation", id],
    queryFn: () => api<Conversation>("GET", `/v1/conversations/${id}`),
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
        <div>
          <strong>{contactName(c.contact)}</strong>
          <div className="muted small">
            +{c.contact.wa_id}
            {number && ` · ${t("inbox.via", { number: number.display_phone_number })}`}
          </div>
        </div>
        <span className={`pill ${c.window.open ? "w-open" : "w-closed"}`}>
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
        <button onClick={() => patch({ status: c.status === "closed" ? "open" : "closed" })}>
          {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
        </button>
        <button className={showNotes ? "active" : ""} onClick={() => setShowNotes(!showNotes)}>{t("inbox.notes")}</button>
      </header>
      {error && <div className="error">{error}</div>}
      <div className="thread-body">
        <div className="messages">
          {msgs.hasNextPage && (
            <button className="link older" onClick={() => msgs.fetchNextPage()}>{t("inbox.older")}</button>
          )}
          {messages.map((m) => (
            <div key={m.id} className={`bubble ${m.direction}`}>
              <div className="bubble-text">{messageText(m)}</div>
              <div className="bubble-meta">
                {shortTime(m.created_at)}
                {m.direction === "outbound" && (
                  <span className={`tick tick-${m.status}`} title={t(`send.status_${m.status}`)}>{statusTick(m.status)}</span>
                )}
                {m.origin === "phone_app" && <span>· {t("inbox.fromPhone")}</span>}
              </div>
              {m.error && <div className="bubble-error">{m.error.message}</div>}
            </div>
          ))}
          <div ref={bottom} />
        </div>
        {showNotes && <Notes id={id} />}
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

function TextComposer({ conv, onSent }: { conv: Conversation; onSent: () => void }) {
  const { t } = useTranslation();
  const replies = useQuickReplies();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // Typing "/shortcut" offers the matching quick replies.
  const slash = text.startsWith("/") && !text.includes(" ") ? text.slice(1).toLowerCase() : null;
  const matches = slash === null ? [] : (replies.data ?? []).filter((r) => r.shortcut.toLowerCase().startsWith(slash));

  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!text.trim()) return;
    setBusy(true);
    setError("");
    try {
      await api("POST", "/v1/messages", { phone_number_id: conv.phone_number_id, to: conv.contact.wa_id, type: "text", text: { body: text } });
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
      {error && <div className="error">{error}</div>}
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        rows={2}
        maxLength={4096}
        placeholder={t("inbox.placeholder")}
      />
      <button className="primary" disabled={busy || !text.trim()}>{t("send.send")}</button>
    </form>
  );
}

function Notes({ id }: { id: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const role = useMe().data?.tenant?.role;
  const replies = useQuickReplies();
  const notes = useQuery({
    queryKey: ["notes", id],
    queryFn: async () => (await api<{ data: Note[] }>("GET", `/internal/inbox/conversations/${id}/notes`)).data,
  });
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

  return (
    <aside className="notes">
      <h3>{t("inbox.notes")}</h3>
      <p className="muted small">{t("inbox.notesHint")}</p>
      {notes.data?.map((n) => (
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
