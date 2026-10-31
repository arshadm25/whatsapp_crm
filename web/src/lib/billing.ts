import type { BillingOverview, Plan, Subscription } from "../api/types";

const inr = new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR", maximumFractionDigits: 2, minimumFractionDigits: 0 });

// Prices are stored in paise.
export function formatPaise(paise: number): string {
  return inr.format(paise / 100);
}

const count = new Intl.NumberFormat("en-IN");

// formatCount writes a plan limit such as 15000 as "15,000".
export function formatCount(n: number): string {
  return count.format(n);
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

// monthlyPrice is what a plan costs a month on the given billing, in paise: the yearly price is
// spread over twelve months. It decides whether a plan change is an upgrade.
export function monthlyPrice(p: Plan, interval: "month" | "year"): number {
  return interval === "year" && p.yearly_price_minor !== null ? p.yearly_price_minor / 12 : p.price_minor;
}

// paysRazorpay reports whether the workspace pays Razorpay for its current plan, so a change
// is made on that subscription rather than by a new checkout.
export function paysRazorpay(b: BillingOverview): boolean {
  const s = b.subscription;
  return !!b.plan && !b.plan.free && !s.admin_granted && s.status === "active" && !s.payment_pending;
}

// canUpgrade reports whether a dearer plan than the current one is on offer.
export function canUpgrade(b: BillingOverview): boolean {
  if (!b.plan || b.plan.free || b.subscription.admin_granted || !b.subscription.usable) return b.plans.some((p) => !p.free);
  const now = monthlyPrice(b.plan, b.subscription.billing_interval);
  return b.plans.some((p) => !p.free && monthlyPrice(p, "month") > now);
}

// nearLimit is the first monthly message or contact limit the workspace has used 80% of, if any.
export function nearLimit(b: BillingOverview): { kind: "messages" | "contacts"; used: number; limit: number } | null {
  const p = b.plan;
  if (!p) return null;
  if (p.messages_per_month !== null && p.messages_per_month > 0 && b.messages_this_month >= 0.8 * p.messages_per_month) {
    return { kind: "messages", used: b.messages_this_month, limit: p.messages_per_month };
  }
  if (p.contacts_limit !== null && p.contacts_limit > 0 && b.contacts >= 0.8 * p.contacts_limit) {
    return { kind: "contacts", used: b.contacts, limit: p.contacts_limit };
  }
  return null;
}

// rupeesToPaise reads an admin's price entry such as "2,999" or "2999.50".
export function rupeesToPaise(v: string): number | null {
  const n = Number(v.replace(/[,\s₹]/g, ""));
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * 100);
}
