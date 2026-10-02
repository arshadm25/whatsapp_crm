export interface TenantInfo {
  id: string;
  name: string;
  slug: string;
  role: "owner" | "admin" | "agent" | "developer";
}

export interface Me {
  user: { id: string; email: string; name: string; email_verified: boolean };
  tenant: TenantInfo | null;
  memberships: TenantInfo[];
}

export interface PublicConfig {
  environment: string;
  meta: { app_id: string; config_id: string; graph_api_version: string };
}

export type OnboardingFlow = "standard" | "coexistence";

export interface OnboardingSession {
  id: string;
  flow: OnboardingFlow;
  step: string;
  state: "awaiting_signup" | "in_progress" | "failed" | "completed" | "cancelled";
  waba_id: string | null;
  phone_number_id: string | null;
  error: { code: string; message: string } | null;
  attempts: number;
  created_at: string;
  updated_at: string;
}

export interface PhoneNumber {
  id: string;
  meta_phone_number_id: string;
  whatsapp_account_id: string;
  waba_id: string;
  display_phone_number: string;
  verified_name: string | null;
  name_status: string | null;
  quality_rating: "green" | "yellow" | "red" | "unknown";
  messaging_limit_tier: string | null;
  is_coexistence: boolean;
  status: "pending" | "connected" | "disconnected" | "revoked" | "error";
  last_synced_at: string | null;
}
