// Meta's messaging limit tiers, lowest first.
export const TIERS = ["TIER_250", "TIER_1K", "TIER_2K", "TIER_10K", "TIER_100K", "TIER_UNLIMITED"];

export function tierValue(tier: string | null) {
  if (!tier) return 0;
  if (tier.includes("UNLIMITED")) return Infinity;
  const n = Number(tier.replace(/\D/g, ""));
  return tier.endsWith("K") ? n * 1000 : n;
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
