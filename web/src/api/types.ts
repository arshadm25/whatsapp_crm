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

export type TemplateStatus = "draft" | "pending" | "approved" | "rejected" | "paused" | "disabled" | "in_appeal";
export type TemplateCategory = "marketing" | "utility" | "authentication";

export interface TemplateComponent {
  type: "HEADER" | "BODY" | "FOOTER" | "BUTTONS";
  format?: string;
  text?: string;
  example?: Record<string, unknown>;
  buttons?: { type: string; text: string; url?: string; phone_number?: string }[];
}

export interface Template {
  id: string;
  meta_template_id: string | null;
  whatsapp_account_id: string;
  name: string;
  language: string;
  category: TemplateCategory;
  status: TemplateStatus;
  rejected_reason: string | null;
  quality_score: string | null;
  parameter_format: "positional" | "named";
  components: TemplateComponent[];
  created_at: string;
  status_updated_at: string | null;
}

export type MessageStatus = "queued" | "sent" | "delivered" | "read" | "failed" | "received" | "deleted";

export interface Message {
  id: string;
  wamid: string | null;
  conversation_id: string;
  phone_number_id: string;
  contact: { id: string; wa_id: string; name: string | null };
  direction: "inbound" | "outbound";
  origin: string;
  type: string;
  content: Record<string, unknown>;
  media_id: string | null;
  status: MessageStatus;
  error: { code: string; message: string; meta_error_code?: number } | null;
  created_at: string;
  status_updated_at: string;
}

export interface Contact {
  id: string;
  wa_id: string;
  name: string | null;
  profile_name: string | null;
  language: string | null;
  custom_fields: Record<string, unknown>;
  tags: string[];
  opt_in_status: "unknown" | "opted_in" | "opted_out";
  opted_in_at: string | null;
  opted_out_at: string | null;
  blocked: boolean;
  created_at: string;
}

export interface Conversation {
  id: string;
  phone_number_id: string;
  contact: Contact;
  status: "open" | "pending" | "closed";
  assignee_id: string | null;
  window: { open: boolean; expires_at: string | null };
  unread_count: number;
  last_message_at: string | null;
  last_message_preview: string | null;
}

export interface Page<T> {
  data: T[];
  next_cursor: string | null;
}

export interface Member {
  id: string;
  name: string;
  email: string;
  role: string;
}

export interface Note {
  id: string;
  body: string;
  author_id: string;
  author_name: string;
  created_at: string;
}

export interface QuickReply {
  id: string;
  shortcut: string;
  body: string;
}

export interface Media {
  id: string;
  mime_type: string;
  size_bytes: number;
  filename: string | null;
  url: string;
  created_at: string;
}
