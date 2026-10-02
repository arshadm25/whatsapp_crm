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

// A location header cannot be filled from a campaign.
export function campaignSupported(components: TemplateComponent[]): boolean {
  return !components.some((c) => c.type.toUpperCase() === "HEADER" && (c.format ?? "").toUpperCase() === "LOCATION");
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

// The body text with each placeholder replaced by its value, for the preview.
export function previewBody(components: TemplateComponent[], vars: Record<string, string>): string {
  const body = components.find((c) => c.type.toUpperCase() === "BODY")?.text ?? "";
  return body.replace(/\{\{\s*([A-Za-z0-9_]+)\s*\}\}/g, (m, p: string) => (vars[p]?.trim() ? previewValue(vars[p]) : m));
}
