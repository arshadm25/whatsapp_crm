// Relative and short dates for tables ("4 minutes ago", "12 Sep").
type T = (k: string, o?: Record<string, unknown>) => string;

export function ago(iso: string, t: T) {
  const mins = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60000));
  if (mins < 1) return t("common.justNow");
  if (mins < 60) return t("common.minutesAgo", { count: mins });
  if (mins < 48 * 60) return t("common.hoursAgo", { count: Math.round(mins / 60) });
  if (mins < 30 * 24 * 60) return t("common.daysAgo", { count: Math.round(mins / (24 * 60)) });
  return new Date(iso).toLocaleDateString();
}

export function shortDate(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

export function shortTime(iso: string) {
  return new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// Two letters for an avatar: first letters of the first and last word.
export function initials(name: string) {
  const parts = name.replace(/[^\p{L}\p{N} ]/gu, "").trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "#";
  return (parts[0][0] + (parts.length > 1 ? parts[parts.length - 1][0] : "")).toUpperCase();
}
