import type { TemplateComponent } from "../api/types";

// Distinct {{variables}} in a template text, in order of first use (same rule as the backend).
export function placeholders(text: string): string[] {
  const seen = new Set<string>();
  for (const m of text.matchAll(/\{\{\s*([A-Za-z0-9_]+)\s*\}\}/g)) seen.add(m[1]);
  return [...seen];
}

export interface TemplateDraft {
  header: string;
  body: string;
  footer: string;
  quickReplies: string[];
  samples: Record<string, string>; // variable -> example value, for body and header
}

// Builds Meta's component list from the editor's fields. Examples are required by Meta's review
// whenever a text has variables.
export function buildComponents(d: TemplateDraft): TemplateComponent[] {
  const out: TemplateComponent[] = [];
  const header = d.header.trim();
  if (header) {
    const vars = placeholders(header);
    const c: TemplateComponent = { type: "HEADER", format: "TEXT", text: header };
    if (vars.length) c.example = { header_text: vars.map((v) => d.samples[`h:${v}`] ?? "") };
    out.push(c);
  }
  const bodyVars = placeholders(d.body);
  const body: TemplateComponent = { type: "BODY", text: d.body.trim() };
  if (bodyVars.length) body.example = { body_text: [bodyVars.map((v) => d.samples[`b:${v}`] ?? "")] };
  out.push(body);
  if (d.footer.trim()) out.push({ type: "FOOTER", text: d.footer.trim() });
  const replies = d.quickReplies.map((r) => r.trim()).filter(Boolean);
  if (replies.length) out.push({ type: "BUTTONS", buttons: replies.map((text) => ({ type: "QUICK_REPLY", text })) });
  return out;
}

// Turns any text into a valid template name: lowercase letters, digits and underscores.
export function toTemplateName(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9_]+/g, "_").replace(/^_+/, "").slice(0, 512);
}

// Number of body variables a stored template expects.
export function bodyVariables(components: TemplateComponent[]): string[] {
  const body = components.find((c) => c.type.toUpperCase() === "BODY");
  return body?.text ? placeholders(body.text) : [];
}
