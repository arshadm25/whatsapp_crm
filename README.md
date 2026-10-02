# Ecogo WhatsApp Platform

Multi-tenant WhatsApp Cloud API platform by Ecogo Software Solutions Pvt Ltd: a dashboard for
businesses and a public REST API, built to qualify Ecogo as a Meta WhatsApp Tech Provider.

Design documents live in [`docs/design`](docs/design) (BRD v0.1, schema v0.1) and the public API
contract in [`api/openapi.yaml`](api/openapi.yaml).

## What is built so far (release 1)

| Area | Status |
| --- | --- |
| D1 Sign-up, login, sessions, email verification; Settings with team members and roles (owners manage everyone, admins manage agents and developers), email invites with a shareable 7-day link, workspace name, legal name and time zone, own name and password change; workspace switcher for people in several workspaces; two-step verification with an authenticator app, asked at every login once on, 5 wrong codes per 15 minutes | Done |
| D2 Connect WhatsApp with Embedded Signup v4, standard and coexistence | Done |
| D5 Phone numbers list (`GET /v1/phone-numbers`) | Done (profile and health sync later) |
| Meta webhook receiver (`ingest`) and event processing: inbound messages, statuses, template results, limit changes, access removal, coexistence echoes and contact sync | Done (history import later) |
| Sending messages (`POST /v1/messages`): opt-out and 24-hour window checks, `Idempotency-Key`, send worker with Meta error mapping, `GET /v1/messages/{id}`, mark as read | Done (API keys later) |
| D8 API keys: create, list and revoke on the Developers screen; `Authorization: Bearer eco_live_…` on every `/v1` endpoint, optionally limited to one number; 60 requests per second per key with `X-RateLimit-*` headers and 429 | Done (sandbox keys later) |
| D8 client webhooks: `/v1/webhook-endpoints` with a signing secret shown once, `message.received`, `message.status`, `template.status` and `number.quality` events signed with `Ecogo-Signature`, retries for about 24 hours, delivery log with retry on the Developers screen | Done |
| D6 Contacts: `/v1/contacts` (upsert by number, filters by tag, consent and search, PATCH for tags, blocking and consent), append-only consent records, STOP and START replies, CSV import with number normalisation, Contacts screen | Done (export and erasure later) |
| D7 Campaigns: `/v1/campaigns` (create with `Idempotency-Key`, list, report, cancel), audience by tags or contact IDs with blocked, opted-out and never-opted-in contacts skipped and reported, template values filled from contact fields with fallbacks, sending throttled to the campaign's rate and the number's messaging limit, live delivery counts, Campaigns screen with audience preview and per-recipient report | Done |
| D9 Billing: every workspace starts on a 14-day trial; plans and prices (in rupees) are set by platform admins in the console; owners choose a plan on Settings, Billing and pay on Razorpay's page, with the first charge at the end of the trial; Razorpay subscription webhooks at `/webhooks/razorpay` keep the status in step; plan prices include GST; plans may name a Razorpay plan for one extra seat, bought as a second subscription whose quantity is the number of extra seats; invites stop when a plan's seats are used and Embedded Signup stops when its numbers are; each confirmed charge gets a numbered GST invoice (CGST and SGST inside the seller's state, IGST outside) shown on Settings, Billing, with the buyer's GSTIN taken from the billing details, and emailed to the workspace owners when issued; platform admins list and export (CSV) invoices across workspaces in the console; plan changes apply from the next cycle and cancelling keeps the paid month; sending and new campaigns stop when the plan has ended or 7 days after a failed charge; admins can extend a trial | Done |
| Phase 2 chatbots: `/v1/bots` with a flow of nodes (message, buttons, question, condition, set, tag, template, handoff, end) started by keyword, first-message, button-reply or any-message triggers, activate, pause, assign to a number, start or stop on a conversation, sessions list, `bot.handoff` webhook; bots never write to blocked or opted-out contacts, outside the 24-hour window (except approved templates), in conversations a person owns, or after the plan has ended, and every bot message is stored with origin `bot`; the Chatbots screen has a flow map, step editors, triggers, activate and pause, and a session log | Done |
| Phase 2 WhatsApp Flows: `/v1/flows` to create, edit, preview, publish, deprecate and delete Flows through Meta's Flows API (Meta's validation errors and a preview link come back on every save), Flow messages through `/v1/messages` or a chatbot's Flow step, Meta's `flows` status webhook, submissions stored against the contact (`/v1/flow-submissions`) and sent as `flow.submission`; a chatbot continues with the submitted answers as variables | Done |
| Phase 2 AI agent: a knowledge base per workspace (`/v1/knowledge/sources`: FAQs, text, one website page, .txt/.md/.csv/.html documents), answers from it with a configurable model (`ECOGO_AI_PROVIDER`, `ECOGO_AI_API_KEY`, default Anthropic Haiku), a confidence threshold and human handoff, the chatbot `ai` step, `/v1/ai/test`, `/v1/ai/usage`, `/v1/ai/logs`, every attempt logged, and a per-plan `ai_replies_per_month` limit (trial: 50) | Done |
| Phase 2 Solution Partner groundwork: Meta's fee usage priced per workspace by category and country from an admin-managed rate card (`ECOGO_META_MARKUP_BP` adds a service charge), a per-workspace switch between paying Meta directly and paying through Ecogo (console, audited), monthly GST statements combining Meta fees with the month's subscription invoices (hourly job issues them a few days after month end, a month waits while a country has no rate), payments recorded by admins | Done |
| Phase 2 Solution Partner billing checks: a daily reconciliation of our billable counts and the rate-card cost against Meta's pricing analytics per day, category and country (mismatches listed in the console), and a credit line step that shares Ecogo's credit line with the WABAs of workspaces paid through Ecogo, off until `ECOGO_META_CREDIT_LINE_ENABLED` | Done |
| D9 Analytics: daily usage rollups by number, pricing category, country and source (so WhatsApp Business app traffic shows apart from API and campaigns), refreshed hourly and on view; Analytics screen with sent, delivered, read, failed and received counts, a per-day chart and an estimate of Meta's charges for billable messages | Done |
| Media: `POST /v1/media` with WhatsApp's type and size limits, `GET /v1/media/{id}` with a 15-minute signed download link, sending uploaded files, copying inbound files from Meta into the MinIO bucket, attachments and previews in the inbox | Done |
| D4 Templates: list, create and edit through Meta review, delete by name, sync from Meta (also at the end of onboarding) | Done |
| Dashboard: Templates list and editor, Send a message test screen | Done |
| A1 Admin console for Ecogo staff (`/admin`, two-step verification required): every workspace with members, numbers, quality, tier and 30-day volume; suspend and reactivate with a reason; Meta webhook health per hour with failures and p95 lag; Meta API errors; the audit log; reading a conversation only with a stated reason, recorded as `message_content.view`. Grant access with `ecogo grant-admin <email>` | Done |
| D3 Inbox: conversations with filters and search, assignment, close and reopen, internal notes, quick replies, template picker when the 24-hour window is closed, live updates over SSE | Done |

## Layout

```
cmd/ecogo/            one binary: `ecogo api | ingest | worker | migrate | grant-admin | revoke-admin`
internal/
  auth/               D1: sign-up, login, sessions, CSRF, email verification, team, invites, 2FA
  admin/              A1: the platform admin console API
  onboarding/         D2: Embedded Signup code exchange (api) and onboarding steps (River worker)
  numbers/            D5: /v1/phone-numbers
  messaging/          Flow 3: /v1/messages and the send worker
  templates/          D4: /v1/templates, template sync from Meta
  credentials/        opens the encrypted Meta tokens
  inbox/              D3: /v1/conversations, notes, quick replies
  media/              /v1/media, signed download links, inbound media copies, uploads to Meta
  storage/            media files in MinIO (S3 API) or a local directory
  events/             live updates: Postgres LISTEN/NOTIFY to Server-Sent Events
  contacts/           D6: /v1/contacts, tags, consent records, CSV import
  campaigns/          D7: /v1/campaigns and the campaign run worker
  bots/               Phase 2: /v1/bots, the flow engine and the bot step worker
  flows/              Phase 2: WhatsApp Flows designer API and Flow submissions
  ai/                 Phase 2: knowledge base, model client and the AI agent
  metafees/           Phase 2: Meta fee rate card, payment mode and monthly statements
  analytics/          D9: usage rollups, the hourly rollup job and the analytics report
  billing/            D9: trials, plans, Razorpay subscriptions and their webhooks
  razorpay/           the only package that calls Razorpay
  devportal/          D8: API keys and their authentication and rate limits
  webhooks/           D8: client webhook endpoints, event outbox and signed deliveries
  metaclient/         the only package that calls Meta's Graph API
  metaevents/         Meta webhooks: signature check and enqueue (ingest), processing (worker)
  crypto/envelope/    AES-256-GCM envelope encryption for Meta tokens and PINs
  db/                 pgx pool, tenant-scoped transactions, migrations
  db/migrations/      SQL migrations (000001 is the design schema)
  db/queries/         sqlc queries; generated code in db/dbq
  jobs/               River (Postgres-backed job queue) setup
  server/             routers for api and ingest, end-to-end tests
web/                  React + Vite dashboard (TanStack Query, React Router, i18next)
deploy/helm/          Helm chart for the Kubernetes cluster
deploy/dev/           local database roles
```

## Run it locally

Needs Go 1.26, Node 22 and Docker.

```sh
cp .env.example .env               # then fill ECOGO_MASTER_KEYS, ECOGO_APP_SECRET and the Meta app
docker compose up -d               # Postgres 16, MinIO and Mailpit (mail UI on http://localhost:8025)
make migrate
make run-api                       # :8080
make run-worker                    # in another terminal
cd web && npm install && npm run dev   # dashboard on http://localhost:5173
```

Embedded Signup only opens on a domain listed in the Meta app's Facebook Login for Business
settings, so test the full Connect WhatsApp flow on staging, or add `localhost` to the
staging app's allowed domains.

## Tests

```sh
make test       # unit tests; database tests skip
make test-db    # everything, against the docker-compose Postgres
cd web && npm test
```

The database tests create a fresh database per test, migrate it as the owner role, and run the
app as `ecogo_app`, so row-level security is enforced exactly as in production. They cover sign-up
and login, CSRF, the standard and coexistence onboarding flows against a fake Graph API, retrying
a failed step, tenant isolation, sending messages (window, opt-out, idempotency, Meta error
mapping, delivery statuses) and the template lifecycle.

## Database roles and tenant isolation

* `ecogo_owner` owns the schema and runs migrations. It has `BYPASSRLS`, which the
  `SECURITY DEFINER` lookup functions (`route_meta_event`, `user_memberships`, ...) need.
* `ecogo_app` is used by api, ingest and worker and is always subject to row-level security.
  Every tenant query runs inside `db.InTenant`, which sets `app.tenant_id` for the transaction.

## Configuration

All settings are `ECOGO_*` environment variables; see [`.env.example`](.env.example) and
[`internal/config/config.go`](internal/config/config.go). Secrets come from a Kubernetes Secret in
the cluster (see `deploy/helm/ecogo-whatsapp/values.yaml`). `ECOGO_MASTER_KEYS` holds versioned
master keys (`1:<base64>,2:<base64>`); the highest version encrypts new data, older ones stay
for decryption during rotation.

## Deploying

```sh
helm upgrade --install ecogo-whatsapp-staging deploy/helm/ecogo-whatsapp \
  -n ecogo-whatsapp-staging --set environment=staging \
  --set hosts.app=staging.whatsapp.ecogo.co.in \
  --set hosts.api=api.staging.whatsapp.ecogo.co.in \
  --set hosts.hooks=hooks.staging.whatsapp.ecogo.co.in \
  --set image.tag=<git sha> --set webImage.tag=<git sha> \
  --set config.metaAppId=<staging app id> --set config.metaConfigId=<configuration id>
```

The chart assumes ingress-nginx, cert-manager and the CloudNativePG operator; each can be switched
off in `values.yaml`. Database migrations run as a Helm hook Job before every upgrade. CI builds
images to GitHub Container Registry on every push to `main`.

### Monitoring

The api, ingest and worker pods serve Prometheus metrics at `/metrics` on their HTTP port: request
counts and latency by route, database pool use, job queue depth and age, and Meta webhook and
Graph API error counts. The ingress does not route `/metrics`, so only the cluster can scrape it.
With the Prometheus Operator installed, set `metrics.podMonitor.enabled=true` and
`metrics.rules.enabled=true` (plus the `labels` your Prometheus selects on) to scrape the pods and
load the alert rules in `templates/monitoring.yaml`.

### Encryption keys

Meta tokens and two-step PINs are envelope-encrypted with the master key in the `master-keys`
Secret (mounted only into api and worker). Keys are versioned (`1:<key>,2:<key>`), so rotating
means adding a new version and re-encrypting. A cloud KMS is not used.
