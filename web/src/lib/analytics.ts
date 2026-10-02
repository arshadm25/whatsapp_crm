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
