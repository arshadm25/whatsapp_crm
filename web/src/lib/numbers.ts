// Meta's messaging limit tiers, lowest first.
export const TIERS = ["TIER_250", "TIER_1K", "TIER_2K", "TIER_10K", "TIER_100K", "TIER_UNLIMITED"];

export function tierValue(tier: string | null) {
  if (!tier) return 0;
  if (tier.includes("UNLIMITED")) return Infinity;
  const n = Number(tier.replace(/\D/g, ""));
  return tier.endsWith("K") ? n * 1000 : n;
}

// The 24-hour messaging limit for a tier, the way the server counts it (numbers.DailyLimit):
// a number with no tier reported yet gets the starting 250; -1 means no limit.
export function dailyLimit(tier: string | null | undefined): number {
  if (!tier) return 250;
  const v = tierValue(tier);
  if (v === Infinity) return -1;
  return TIERS.includes(tier.toUpperCase()) ? v : 250;
}

// "2,000" or "Unlimited" for a tier name.
export function tierLabel(tier: string | null, unlimited: string) {
  if (!tier) return "—";
  const v = tierValue(tier);
  return v === Infinity ? unlimited : v.toLocaleString();
}

export function highestTier(tiers: (string | null)[]) {
  return [...tiers].sort((a, b) => tierValue(b) - tierValue(a))[0] ?? null;
}

// How a number stands with WhatsApp Cloud API registration, for the Numbers table.
// A number shared with the WhatsApp Business app (coexistence) stays registered to the app and is
// never registered here, so for it a missing date is normal, not a problem.
export type Registration = "registered" | "businessApp" | "notRegistered" | "none";

export function registrationState(n: { status: string; is_coexistence: boolean; registered_at?: string | null }): Registration {
  if (n.registered_at) return "registered";
  if (n.is_coexistence) return "businessApp";
  if (n.status === "connected" || n.status === "pending") return "notRegistered";
  return "none";
}

// How full today's messaging limit is: "ok", "near" from 80% and "full" at the limit.
// A negative limit means no limit.
export function usageLevel(used: number, limit: number): "ok" | "near" | "full" {
  if (limit < 0 || limit === 0) return "ok";
  if (used >= limit) return "full";
  return used / limit >= 0.8 ? "near" : "ok";
}

// Percentage of today's limit used, 0 to 100, for a meter.
export function usagePct(used: number, limit: number) {
  if (limit <= 0) return 0;
  return Math.min(100, Math.round((used / limit) * 100));
}
