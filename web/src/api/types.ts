export interface TenantInfo {
  id: string;
  name: string;
  slug: string;
  role: "owner" | "admin" | "agent" | "developer";
}

export interface Me {
  user: {
    id: string;
    email: string;
    name: string;
    email_verified: boolean;
    two_factor_enabled: boolean;
    is_platform_admin: boolean;
  };
  tenant: TenantInfo | null;
  memberships: TenantInfo[];
  // Set, alone, while a login still needs its two-step code.
  mfa_required?: boolean;
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

export interface BusinessProfile {
  about: string;
  address: string;
  description: string;
  email: string;
  vertical: string;
  websites: string[];
  profile_picture_url: string;
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

export interface APIKey {
  id: string;
  name: string;
  prefix: string;
  mode: "live" | "sandbox";
  phone_number_id: string | null;
  last_used_at: string | null;
  revoked_at: string | null;
  created_at: string;
}

export interface CreatedAPIKey extends APIKey {
  key: string;
}

export type WebhookEventType = "message.received" | "message.status" | "template.status" | "number.quality";

export interface WebhookEndpoint {
  id: string;
  url: string;
  description: string | null;
  event_types: WebhookEventType[];
  phone_number_id: string | null;
  enabled: boolean;
  created_at: string;
}

export interface CreatedWebhookEndpoint extends WebhookEndpoint {
  secret: string;
}

export interface WebhookDelivery {
  id: string;
  event_id: string;
  event_type: WebhookEventType;
  status: "pending" | "succeeded" | "retrying" | "dead";
  attempt_count: number;
  last_response_code: number | null;
  last_error: string | null;
  next_attempt_at: string | null;
  created_at: string;
}

export interface Tag {
  id: string;
  name: string;
  contacts: number;
}

export interface ConsentEvent {
  kind: "opt_in" | "opt_out";
  source: string;
  evidence: string | null;
  recorded_by_name: string | null;
  occurred_at: string;
}

export interface ImportResult {
  rows: number;
  created: number;
  updated: number;
  skipped: number;
  opted_in: number;
  opted_out: number;
  errors: { line: number; message: string }[];
}

export type CampaignStatus = "draft" | "scheduled" | "running" | "paused" | "completed" | "cancelled" | "failed";

export interface CampaignStats {
  total: number;
  pending: number;
  skipped: number;
  queued: number;
  sent: number;
  delivered: number;
  read: number;
  failed: number;
}

export interface Campaign {
  id: string;
  name: string;
  status: CampaignStatus;
  phone_number_id: string;
  template_id: string;
  template: { name: string; language: string; variables: Record<string, string> };
  audience: { tags?: string[]; contact_ids?: string[] };
  scheduled_at: string | null;
  started_at: string | null;
  finished_at: string | null;
  send_rate_per_min: number;
  stats: CampaignStats;
  created_at: string;
}

export interface AudienceCounts {
  total: number;
  eligible: number;
  blocked: number;
  opted_out: number;
  no_opt_in: number;
}

export type RecipientStatus = "pending" | "skipped" | "queued" | "sent" | "delivered" | "read" | "failed";

export interface Recipient {
  contact_id: string;
  wa_id: string;
  name: string | null;
  status: RecipientStatus;
  skip_reason: string | null;
  message_id: string | null;
  error_code: number | null;
  updated_at: string;
}

export interface UsageCounts {
  sent: number;
  delivered: number;
  read: number;
  failed: number;
  received: number;
  billable: number;
  est_cost_minor: number;
}

export interface UsageReport {
  from: string;
  to: string;
  time_zone: string;
  currency: string;
  totals: UsageCounts;
  unpriced_billable: number;
  days: (UsageCounts & { day: string })[];
  by_category: (UsageCounts & { key: string })[];
  by_origin: (UsageCounts & { key: string })[];
  by_country: (UsageCounts & { key: string })[];
  by_number: (UsageCounts & { key: string })[];
}

export type Role = TenantInfo["role"];

export interface TeamMember {
  id: string;
  name: string;
  email: string;
  role: Role;
  last_login_at: string | null;
  joined_at: string;
}

export interface Invite {
  id: string;
  email: string;
  role: Role;
  invited_by_name: string;
  expires_at: string;
  created_at: string;
  link?: string;
}

export interface InviteInfo {
  tenant_name: string;
  email: string;
  role: Role;
  invited_by_name: string;
  account_exists: boolean;
}

export interface Workspace {
  id: string;
  name: string;
  legal_name: string | null;
  time_zone: string;
}

// Platform admin console (A1).
export interface AdminTenant {
  id: string;
  name: string;
  slug: string;
  legal_name: string | null;
  status: "active" | "suspended" | "closed";
  suspended_reason: string | null;
  time_zone: string;
  created_at: string;
}

export interface AdminTenantDetail extends AdminTenant {
  members: number;
  numbers: {
    id: string;
    display_phone_number: string;
    verified_name: string | null;
    status: string;
    quality_rating: string;
    messaging_limit_tier: string | null;
    coexistence: boolean;
    waba_id: string;
  }[];
  sent_30d: number;
  received_30d: number;
  last_message_at: string | null;
  webhook_deliveries_24h: Record<string, number>;
  subscription: Subscription | null;
}

export interface AdminConversation {
  id: string;
  phone_number_id: string;
  contact_wa_id: string;
  status: string;
  last_message_at: string | null;
}

export interface WebhookHealth {
  hours: { hour: string; received: number; processed: number; failed: number; pending: number; p95_lag_seconds: number }[];
  errors: { id: number; received_at: string; field: string | null; waba_id: string | null; tenant_id: string | null; error: string | null }[];
}

export interface MetaApiError {
  id: number;
  tenant_id: string | null;
  method: string;
  path: string;
  http_status: number;
  code: number | null;
  subcode: number | null;
  message: string | null;
  fbtrace_id: string | null;
  occurred_at: string;
}

export interface AuditEntry {
  id: number;
  tenant_id: string | null;
  actor_type: string;
  actor_id: string | null;
  actor_email: string | null;
  action: string;
  target_type: string | null;
  target_id: string | null;
  reason: string | null;
  ip: string | null;
  occurred_at: string;
}

// D9 billing.
export interface Plan {
  code: string;
  name: string;
  price_minor: number;
  currency: string;
  included_numbers: number;
  included_seats: number;
  extra_seat_minor: number;
  razorpay_plan_id?: string | null;
  extra_seat_razorpay_plan_id?: string | null;
  extra_seats_available: boolean;
  sort_order: number;
  is_active: boolean;
}

export interface Subscription {
  status: "trialing" | "active" | "past_due" | "cancelled";
  plan_code: string | null;
  current_period_start: string;
  current_period_end: string;
  cancel_at_period_end: boolean;
  usable: boolean;
  payment_pending: boolean;
  extra_seats: number;
}

export interface AdminInvoice {
  id: string;
  tenant_id: string;
  tenant_name: string;
  number: string;
  description: string;
  buyer_name: string;
  buyer_gstin: string;
  place_of_supply: string;
  taxable_minor: number;
  cgst_minor: number;
  sgst_minor: number;
  igst_minor: number;
  total_minor: number;
  razorpay_payment_id: string;
  issued_at: string;
  emailed: boolean;
}

export interface Invoice {
  id: string;
  number: string;
  description: string;
  total_minor: number;
  issued_at: string;
}

export interface BillingProfile {
  legal_name: string;
  gstin: string;
  state_code: string;
  address: string;
}

export interface BillingOverview {
  subscription: Subscription;
  plan: Plan | null;
  plans: Plan[];
  connected_numbers: number;
  seats: number;
  payments_enabled: boolean;
}
