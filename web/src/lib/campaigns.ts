import type { TemplateComponent } from "../api/types";
import { placeholders } from "./templates";

// One value a campaign must supply for its template, keyed the way the API expects
// (same rules as internal/campaigns/render.go).
export interface Slot {
  key: string; // "1", "first_name", "header.1", "header", "button.0"
  part: "header" | "body" | "button";
  media?: "image" | "video" | "document";
}

export function templateSlots(components: TemplateComponent[]): Slot[] {
  const out: Slot[] = [];
  for (const c of components) {
    const type = c.type.toUpperCase();
    if (type === "HEADER") {
      const f = (c.format ?? "TEXT").toUpperCase();
      if (f === "IMAGE" || f === "VIDEO" || f === "DOCUMENT") {
        out.push({ key: "header", part: "header", media: f.toLowerCase() as Slot["media"] });
      } else if (f === "TEXT") {
        for (const p of placeholders(c.text ?? "")) out.push({ key: `header.${p}`, part: "header" });
      }
    } else if (type === "BODY") {
      for (const p of placeholders(c.text ?? "")) out.push({ key: p, part: "body" });
    } else if (type === "BUTTONS") {
      (c.buttons ?? []).forEach((b, i) => {
        if (b.type.toUpperCase() === "URL" && b.url && placeholders(b.url).length > 0) {
          out.push({ key: `button.${i}`, part: "button" });
        }
      });
    }
  }
  return out;
}

// A location header or a carousel cannot be filled from a campaign.
export function campaignSupported(components: TemplateComponent[]): boolean {
  return !components.some(
    (c) =>
      c.type.toUpperCase() === "CAROUSEL" ||
      (c.type.toUpperCase() === "HEADER" && (c.format ?? "").toUpperCase() === "LOCATION"),
  );
}

// Contact fields offered in the variable picker.
export const CONTACT_FIELDS = ["first_name", "name", "wa_id", "language"] as const;

export function fieldToken(field: string, fallback = ""): string {
  return fallback ? `{{contact.${field}|${fallback}}}` : `{{contact.${field}}}`;
}

// Fills a value for the preview: contact fields show as their fallback, or as [field].
export function previewValue(v: string): string {
  return v.replace(/\{\{\s*contact\.([A-Za-z0-9_]+)\s*(?:\|([^{}]*))?\}\}/g, (_, f: string, fb?: string) =>
    fb?.trim() ? `[${f} or "${fb.trim()}"]` : `[${f}]`,
  );
}

// Text with each placeholder replaced by its value, for the preview. prefix is "header." for a
// text header, whose values are keyed header.1, header.name and so on.
export function previewText(text: string, vars: Record<string, string>, prefix = ""): string {
  return text.replace(/\{\{\s*([A-Za-z0-9_]+)\s*\}\}/g, (m, p: string) => (vars[prefix + p]?.trim() ? previewValue(vars[prefix + p]) : m));
}

// The body text with each placeholder replaced by its value, for the preview.
export function previewBody(components: TemplateComponent[], vars: Record<string, string>): string {
  return previewText(components.find((c) => c.type.toUpperCase() === "BODY")?.text ?? "", vars);
}

// How long sending is likely to take, for the review step. The worker queues messages at the
// campaign's rate and never past the number's 24-hour messaging limit (limit < 0: no limit), so
// an audience larger than the limit is spread over days. Other business-started messages from
// the number count against the same limit, which can make it slower than this.
export type SendEstimate = { kind: "minutes"; value: number } | { kind: "days"; value: number };

export function estimateSend(recipients: number, ratePerMin: number, dailyLimit: number): SendEstimate | null {
  if (recipients <= 0 || ratePerMin <= 0) return null;
  if (dailyLimit >= 0 && recipients > dailyLimit) {
    return { kind: "days", value: dailyLimit > 0 ? Math.ceil(recipients / dailyLimit) : Infinity };
  }
  return { kind: "minutes", value: Math.max(1, Math.ceil(recipients / ratePerMin)) };
}
