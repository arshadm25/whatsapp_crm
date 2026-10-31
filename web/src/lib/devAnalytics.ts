import type { SeriesMeta } from "../api/types";

// Chart helpers for the Developers pages. The server sends only buckets that have data; these
// lay out every bucket in the range so empty hours and days show as zero.

export type Bucket = { key: string; label: string };

function dayKey(d: Date, timeZone: string) {
  // en-CA formats as YYYY-MM-DD.
  return new Intl.DateTimeFormat("en-CA", { timeZone, year: "numeric", month: "2-digit", day: "2-digit" }).format(d);
}

function safeZone(tz: string) {
  try {
    new Intl.DateTimeFormat("en-CA", { timeZone: tz });
    return tz;
  } catch {
    return "UTC";
  }
}

// bucketKey is the key a server bucket timestamp falls in.
export function bucketKey(meta: SeriesMeta, iso: string) {
  const d = new Date(iso);
  if (meta.hourly) return String(Math.floor(d.getTime() / 3600_000));
  return dayKey(d, safeZone(meta.time_zone));
}

// buckets lists every bucket from meta.from up to now, oldest first.
export function buckets(meta: SeriesMeta, now = new Date()): Bucket[] {
  const tz = safeZone(meta.time_zone);
  const out: Bucket[] = [];
  const start = new Date(meta.from).getTime();
  if (meta.hourly) {
    const first = Math.floor(start / 3600_000);
    const last = Math.floor(now.getTime() / 3600_000);
    for (let h = first; h <= last && out.length < 48; h++) {
      const d = new Date(h * 3600_000);
      out.push({ key: String(h), label: new Intl.DateTimeFormat(undefined, { timeZone: tz, hour: "2-digit", minute: "2-digit" }).format(d) });
    }
    return out;
  }
  const seen = new Set<string>();
  // Step from midday so a daylight-saving change never skips or repeats a day.
  for (let t = start + 12 * 3600_000; t <= now.getTime() + 12 * 3600_000 && out.length < 120; t += 24 * 3600_000) {
    const d = new Date(t);
    const key = dayKey(d, tz);
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ key, label: new Intl.DateTimeFormat(undefined, { timeZone: tz, day: "numeric", month: "short" }).format(d) });
  }
  return out.slice(0, meta.days);
}

// fill sums each point's values into its bucket; buckets without points are zero.
export function fill<P extends { bucket: string }>(meta: SeriesMeta, list: Bucket[], points: P[], value: (p: P) => number): number[] {
  const index = new Map(list.map((b, i) => [b.key, i]));
  const out = list.map(() => 0);
  for (const p of points) {
    const i = index.get(bucketKey(meta, p.bucket));
    if (i !== undefined) out[i] += value(p);
  }
  return out;
}

export function pct(part: number, whole: number): string {
  if (!whole) return "—";
  const v = (part * 100) / whole;
  return `${v >= 10 || v === 0 ? Math.round(v) : v.toFixed(1)}%`;
}

export function ms(v: number): string {
  if (!v) return "—";
  if (v >= 1000) return `${(v / 1000).toFixed(1)} s`;
  return `${Math.round(v)} ms`;
}

// statusTone picks the pill colour for an HTTP status.
export function statusTone(status: number): "ok" | "wa" | "er" {
  if (status >= 500) return "er";
  if (status >= 400) return "wa";
  return "ok";
}
