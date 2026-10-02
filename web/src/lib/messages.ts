import type { Message } from "../api/types";

type Obj = Record<string, unknown>;
const obj = (v: unknown): Obj => (v && typeof v === "object" ? (v as Obj) : {});
const str = (v: unknown): string => (typeof v === "string" ? v : "");

// One line of readable text for a message, whatever its type. Inbound messages store Meta's
// object and outbound ones the object we sent, which share field names.
export function messageText(m: Pick<Message, "type" | "content">): string {
  const c = obj(m.content);
  const part = obj(c[m.type]);
  switch (m.type) {
    case "text":
      return str(part.body);
    case "image":
    case "video":
    case "document":
      return [`[${m.type}]`, str(part.caption) || str(part.filename)].filter(Boolean).join(" ");
    case "audio":
    case "sticker":
      return `[${m.type}]`;
    case "location":
      return `[location] ${str(part.name) || `${part.latitude}, ${part.longitude}`}`;
    case "template":
      return `Template: ${str(part.name)}`;
    case "reaction":
      return `Reacted ${str(part.emoji)}`;
    case "button":
      return str(part.text);
    case "interactive": {
      const reply = obj(part.button_reply ?? part.list_reply);
      return str(reply.title) || str(obj(part.body).text) || "[interactive]";
    }
    default:
      return `[${m.type}]`;
  }
}

// The caption under a media message's file in the thread.
export function captionOf(m: Pick<Message, "type" | "content">): string {
  return str(obj(obj(m.content)[m.type]).caption);
}

// The tick shown next to an outbound message.
export function statusTick(status: Message["status"]): string {
  return { queued: "🕓", sent: "✓", delivered: "✓✓", read: "✓✓", failed: "⚠", received: "", deleted: "" }[status];
}
