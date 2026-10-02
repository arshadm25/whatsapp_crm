# Ecogo WhatsApp Platform

Multi-tenant WhatsApp Cloud API platform by Ecogo Software Solutions Pvt Ltd: a dashboard for
businesses and a public REST API, built to qualify Ecogo as a Meta WhatsApp Tech Provider.

Design documents live in [`docs/design`](docs/design) (BRD v0.1, schema v0.1) and the public API
contract in [`api/openapi.yaml`](api/openapi.yaml).

## What is built so far (release 1)

| Area | Status |
| --- | --- |
| D1 Sign-up, login, sessions, email verification | Done (invites, roles screen and 2FA later) |
| D2 Connect WhatsApp with Embedded Signup v4, standard and coexistence | Done |
| D5 Phone numbers list (`GET /v1/phone-numbers`) | Done (profile and health sync later) |
| Meta webhook receiver (`ingest`) and event processing: inbound messages, statuses, template results, limit changes, access removal, coexistence echoes and contact sync | Done (history import later) |
| Sending messages (`POST /v1/messages`): opt-out and 24-hour window checks, `Idempotency-Key`, send worker with Meta error mapping, `GET /v1/messages/{id}`, mark as read | Done (API keys later) |
| D8 API keys: create, list and revoke on the Developers screen; `Authorization: Bearer eco_live_…` on every `/v1` endpoint, optionally limited to one number; 60 requests per second per key with `X-RateLimit-*` headers and 429 | Done (sandbox keys later) |
| D8 client webhooks: `/v1/webhook-endpoints` with a signing secret shown once, `message.received`, `message.status`, `template.status` and `number.quality` events signed with `Ecogo-Signature`, retries for about 24 hours, delivery log with retry on the Developers screen | Done |
| Media: `POST /v1/media` with WhatsApp's type and size limits, `GET /v1/media/{id}` with a 15-minute signed download link, sending uploaded files, copying inbound files from Meta into the MinIO bucket, attachments and previews in the inbox | Done |
| D4 Templates: list, create and edit through Meta review, delete by name, sync from Meta (also at the end of onboarding) | Done |
| Dashboard: Templates list and editor, Send a message test screen | Done |
| D3 Inbox: conversations with filters and search, assignment, close and reopen, internal notes, quick replies, template picker when the 24-hour window is closed, live updates over SSE | Done |

## Layout

```
cmd/ecogo/            one binary: `ecogo api | ingest | worker | migrate`
internal/
  auth/               D1: sign-up, login, sessions, CSRF, email verification
  onboarding/         D2: Embedded Signup code exchange (api) and onboarding steps (River worker)
  numbers/            D5: /v1/phone-numbers
  messaging/          Flow 3: /v1/messages and the send worker
  templates/          D4: /v1/templates, template sync from Meta
  credentials/        opens the encrypted Meta tokens
  inbox/              D3: /v1/conversations, notes, quick replies
  media/              /v1/media, signed download links, inbound media copies, uploads to Meta
  storage/            media files in MinIO (S3 API) or a local directory
  events/             live updates: Postgres LISTEN/NOTIFY to Server-Sent Events
  contacts/           D6: contact representation (more with the contacts slice)
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
