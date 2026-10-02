import type { Subscription } from "../api/types";

const inr = new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR", maximumFractionDigits: 2, minimumFractionDigits: 0 });

// Prices are stored in paise.
export function formatPaise(paise: number): string {
  return inr.format(paise / 100);
}

// Whole days left until end, rounded up; 0 once it has passed.
export function daysLeft(end: string, now: Date = new Date()): number {
  const ms = new Date(end).getTime() - now.getTime();
  return ms <= 0 ? 0 : Math.ceil(ms / 86_400_000);
}

// The day sending stops after a failed charge (the server allows 7 days of grace).
export function graceEnd(sub: Subscription): Date {
  return new Date(new Date(sub.current_period_end).getTime() + 7 * 86_400_000);
}

// rupeesToPaise reads an admin's price entry such as "2,999" or "2999.50".
export function rupeesToPaise(v: string): number | null {
  const n = Number(v.replace(/[,\s₹]/g, ""));
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * 100);
}
