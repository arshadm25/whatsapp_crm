// Formatting for the Analytics screen.

// Rupees from paise, e.g. 12345 -> "₹123.45".
export function rupees(paise: number): string {
  return new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR" }).format(paise / 100);
}

// A share as a whole percentage, or "—" when there is nothing to divide by.
export function percent(part: number, whole: number): string {
  return whole > 0 ? `${Math.round((part / whole) * 100)}%` : "—";
}

// YYYY-MM-DD for a date n days before today in the browser's calendar.
export function daysAgo(n: number, today = new Date()): string {
  const d = new Date(today.getFullYear(), today.getMonth(), today.getDate() - n);
  const p = (x: number) => String(x).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// Bar heights scaled to the tallest day, as fractions of the chart height.
export function scale(values: number[]): number[] {
  const max = Math.max(0, ...values);
  return values.map((v) => (max > 0 ? v / max : 0));
}

// A wait in seconds as "45s", "3m 20s" or "2h 5m"; "—" when there is none.
export function duration(seconds: number | null | undefined): string {
  if (seconds == null) return "—";
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

// The daily figures as CSV for the Export button.
export function reportCSV(days: { day: string; sent: number; delivered: number; read: number; failed: number; received: number; billable: number; est_cost_minor: number }[]): string {
  const rows = [["date", "sent", "delivered", "read", "failed", "received", "billable", "estimated_cost_inr"]];
  for (const d of days) {
    rows.push([d.day, d.sent, d.delivered, d.read, d.failed, d.received, d.billable, (d.est_cost_minor / 100).toFixed(2)].map(String));
  }
  return rows.map((r) => r.join(",")).join("\n") + "\n";
}
