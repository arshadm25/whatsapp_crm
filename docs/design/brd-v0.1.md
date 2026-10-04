# WhatsApp Tech Provider Platform: Business Requirements Document

Oct 2, 2026 · @Muhammed Arshad

## Summary

We will build a multi-tenant SaaS platform (dashboard plus public REST API) that connects directly to Meta's WhatsApp Cloud API, so that we qualify as an independent Meta **Tech Provider** without a middleman BSP such as Twilio or 360dialog. The first release is shaped around what Meta's App Review needs to see: Embedded Signup onboarding, live message send and receive, and template management, all running in our own UI.

Clients onboard themselves through Embedded Signup, own their WhatsApp Business Accounts (WABAs), and pay Meta directly for messaging. We provide the software, the API, the webhook routing and the inbox.

**How we earn.** As a Tech Provider we cannot add a commission to Meta's message fees, because clients pay Meta directly and Meta pays Tech Providers nothing. Our revenue therefore comes from our own charges, billed separately from Meta's:

| Revenue stream | When | How it is charged |
| --- | --- | --- |
| Platform subscription | From launch | Monthly fee per connected number or per plan tier |
| Seats | From launch | Fee per agent user above the plan's included seats |
| Platform usage fee | From launch | Our own small fee per message or per campaign recipient, shown as our charge, not as a Meta fee; not recommended at launch (see pricing question) |
| Add-ons | Phase 2 onwards | Chatbot, Flows and AI replies as paid add-ons |
| Markup on Meta fees | After Solution Partner approval | We pay Meta and re-bill clients for Meta fees plus our margin (see Solution Partner track) |

The amounts are still open; see the pricing question under Open questions.

| Field | Value |
| --- | --- |
| Document | Business Requirements Document (BRD), version 0.1 draft |
| Owner | Muhammed Arshad |
| Status | Draft for review; applicant company is Ecogo AI Technologies Pvt Ltd; other details still open (see Open questions) |
| Source inputs | Owner's brief in this project, Meta developer documentation |

## Objectives, scope and success metrics

The business goal is approval as a Meta Tech Provider, followed by a sellable WhatsApp platform for many independent client businesses.

**Objectives**

1. Pass Meta Business Verification, App Review (Advanced Access for both WhatsApp permissions) and Access Verification on the first or second submission.
2. Lift our onboarding cap from 10 to 200 new client businesses per rolling 7 days, which Meta grants once all three checks pass.
3. Let a client go from sign-up to sending a first WhatsApp message without our staff touching the setup.
4. Expose every dashboard capability through a documented public API so clients can integrate their own systems.

**In scope for release 1**

- Embedded Signup (v4) onboarding, token exchange and secure token storage
- Multi-tenant webhook receiver and router
- Shared team inbox: send and receive text, media and template messages
- Template manager: create, submit, track status, delete
- Phone number and WABA overview: status, quality rating, messaging limit
- Contacts with opt-in tracking, and basic broadcast campaigns
- Public REST API with API keys, plus outbound webhooks to client systems
- Admin console for our own team: tenants, usage, audit log

**Out of scope for release 1**

- Acting as a Solution Partner (reselling Meta credit lines and invoicing clients for Meta fees): groundwork and the Meta application are planned in Phase 2 (see Solution Partner track under Roadmap); live reselling follows Meta's approval
- Chatbot builder, WhatsApp Flows designer and AI agents (candidates for release 2): now planned in Phase 2, see Chatbot and automation track under Roadmap
- Instagram, Messenger, SMS or other channels
- WhatsApp Business App coexistence onboarding (release 2): now planned in Phase 1, see Coexistence track under Roadmap

**Success metrics**

| Metric | Target |
| --- | --- |
| Meta App Review outcome | Approved within 2 submissions |
| Time from Embedded Signup start to first message sent | Under 10 minutes for a client with a verified business |
| Embedded Signup completion rate | 70% or more of started flows |
| Webhook processing | 99% of inbound events routed to the right tenant within 2 seconds |
| Platform availability | 99.9% monthly for API and webhook receiver |

## Meta Tech Provider requirements

Meta approves a Tech Provider in three gates: Business Verification of our company, App Review of our Meta app, and Access Verification. Until all three pass, Embedded Signup is capped at 10 new client businesses per rolling 7 days; after, the cap is 200.

| Requirement | What Meta expects | What we must build or provide |
| --- | --- | --- |
| Meta app | A Business-type app in Meta for Developers with the WhatsApp use case, linked to our business portfolio | Create the app; record App ID, App Secret and Configuration ID in a secrets vault |
| Business Verification | Legal entity verified in Meta Business Suite with registration or tax documents; the website and domain must match the legal name | Company registration certificate, tax ID, utility bill or bank statement, live company website on our own domain with privacy policy and terms |
| App Review: whatsapp\_business\_messaging | Advanced Access; screen recording of a message composed in our UI and received on a real WhatsApp client | Inbox module that sends a live message (Demo 1) |
| App Review: whatsapp\_business\_management | Advanced Access; screen recording of a template created in our UI and submitted to Meta | Template manager that calls Meta's template API (Demo 2) |
| App settings for review | Privacy policy URL, terms URL, app icon, category, data deletion instructions or callback, contact email | Public legal pages and a data deletion endpoint |
| Access Verification | Confirms we are a legitimate provider serving other businesses | Business description, how clients use the platform, our website |
| Facebook Login for Business | A login configuration for Embedded Signup, with our production HTTPS domains on the allowed list | Configuration ID; allowed domains for dashboard and staging |
| Embedded Signup v4 | Clients connect their own WABA and number through Meta's popup on our site. v2 and v3 are deprecated on 15 Oct 2026, so we build on v4 only | Connect WhatsApp button, popup handler, server-side code exchange |
| Webhooks | One HTTPS callback per app, GET verification handshake, every POST signed with X-Hub-Signature-256 (HMAC-SHA256 of the body with the App Secret) | Webhook receiver with signature check, tenant routing, retries and idempotency |
| Client billing | Tech Provider clients add their own payment method and pay Meta directly | Show clients where to add payment in Meta Business Suite; track spend estimates only |

**What happens after a client finishes Embedded Signup.** Each step is a requirement on our backend:

1. Receive the authorization code and the WABA ID, phone number ID and business ID from the popup's session event.
2. Exchange the code server-side for a Business Integration System User (BISU) token scoped to that client. This token does not expire but can be revoked by the client.
3. Encrypt and store the token against the tenant, WABA ID and phone number ID.
4. Subscribe our app to the client's WABA webhooks (subscribed\_apps).
5. Register the phone number for Cloud API with a two-step verification PIN.
6. Fetch number details (display name, quality rating, messaging limit) and show the number as Connected.

**Platform rules the product must respect**

- Pricing changes on 1 Oct 2026: service replies inside the 24-hour window become chargeable after 1,000 free service messages per phone number per month. Marketing, utility and authentication templates are charged per delivered message by recipient country.
- Messaging limits apply per business portfolio, shared by all its numbers: 250, then 2,000, 10,000, 100,000 and unlimited unique users per 24 hours, raised automatically on good quality.
- Free-form messages are allowed only inside 24 hours of the customer's last message; outside that window only approved templates can be sent.
- Businesses must hold opt-in consent before messaging a customer first.

## Stakeholders and personas

Four roles use the platform; the dashboard's permissions follow them.

| Persona | Who they are | What they need |
| --- | --- | --- |
| Platform admin (us) | Our operations and support staff | See all tenants, usage, webhook health and audit logs; suspend a tenant; never read message content without a logged reason |
| Tenant owner | The client business owner who connects WhatsApp | Sign up, run Embedded Signup, invite team, manage billing and API keys |
| Tenant agent | Client staff answering customers | Shared inbox, assignment, quick replies, send templates when the 24-hour window is closed |
| Tenant developer | Client engineer integrating their systems | API keys, API docs, sandbox, outbound webhook configuration and delivery logs |

External stakeholders are Meta (reviewer and platform owner) and the end customers who message our clients on WhatsApp.

## Dashboard requirements

The dashboard is a web app with nine client modules and an admin console; the ones marked Must are required for Meta's App Review or for a working client onboarding.

| ID | Module | Key requirements | Priority |
| --- | --- | --- | --- |
| D1 | Account and team | Email sign-up with verification, organisation (tenant) creation, invite users, roles: owner, admin, agent, developer; optional 2FA | Must |
| D2 | Connect WhatsApp | Connect WhatsApp button launching Embedded Signup v4; progress and error states (cancelled, failed, number already registered); list of connected WABAs and numbers; disconnect and reconnect | Must |
| D3 | Inbox | Conversation list by number, real-time incoming messages, send text, image, document, audio and video; delivery ticks (sent, delivered, read, failed); 24-hour window indicator; switch to template when window is closed; assign to agent; notes; quick replies | Must |
| D4 | Templates | Create template (name, category marketing, utility or authentication, language, header, body with variables, footer, buttons); preview; submit to Meta; live status (pending, approved, rejected, paused) with rejection reason; edit and delete; sync existing templates from the WABA | Must |
| D5 | Numbers and health | Per number: display name and its approval status, quality rating, messaging limit tier, registration status; business profile edit (about, address, website, logo) | Must |
| D6 | Contacts | Import CSV, tags, custom fields, opt-in status with source and timestamp, opt-out handling (STOP keyword), block list | Should |
| D7 | Campaigns | Send an approved template to a segment, schedule, throttle within messaging limit, per-campaign delivery and read report | Should |
| D8 | Developer settings | Create and revoke API keys, outbound webhook URLs and secrets, delivery log with retry, link to API docs | Must |
| D9 | Analytics and usage | Messages by category and country, estimated Meta cost, free service allowance used per number, conversation response times | Should |
| A1 | Platform admin console | Tenant list and status, suspend or reactivate, webhook throughput and error rates, Meta API error log, audit log of admin actions | Must |

**Acceptance criteria for the App Review modules**

- [ ] D2: a new client with a Facebook account can connect a WABA and number end to end, and the number shows Connected without staff help.
- [ ] D3: a message typed in the inbox arrives on a real phone, and the reply from that phone appears in the inbox within 5 seconds.
- [ ] D4: a template created in the UI appears in Meta's WhatsApp Manager as pending, and its approval status updates in our UI from the webhook, not by manual refresh.

## Public API and webhooks

Clients get a versioned REST API (/v1) that wraps Meta's Graph API, so they never handle Meta tokens; our backend holds every BISU token and calls Meta on their behalf.

**API principles**

- JSON over HTTPS, authenticated with tenant API keys (Bearer), scoped to the tenant and optionally to one phone number.
- Idempotency-Key header on all POSTs that send messages.
- Per-key rate limits, with Meta's own limits surfaced as clear errors (for example, messaging limit reached or 24-hour window closed).
- OpenAPI 3 specification, published docs, and a sandbox key that returns realistic responses without sending.

**Endpoints for release 1**

| Area | Method and path | Purpose |
| --- | --- | --- |
| Messages | POST /v1/messages | Send text, media, template, interactive (buttons, lists) or reaction message from a phone number |
| Messages | GET /v1/messages/{id} | Message status and error detail |
| Messages | POST /v1/messages/{id}/read | Mark an inbound message as read |
| Conversations | GET /v1/conversations | List conversations with window state and assignee |
| Media | POST /v1/media, GET /v1/media/{id} | Upload media for sending; download inbound media |
| Templates | GET, POST /v1/templates | List and create templates (submitted to Meta) |
| Templates | DELETE /v1/templates/{name} | Delete a template |
| Numbers | GET /v1/phone-numbers | Numbers with quality rating, limit tier and status |
| Numbers | GET, PATCH /v1/phone-numbers/{id}/profile | Read and update the business profile |
| Contacts | GET, POST, PATCH /v1/contacts | Manage contacts, tags and opt-in records |
| Campaigns | POST /v1/campaigns, GET /v1/campaigns/{id} | Create a template broadcast and read its report |
| Webhooks | GET, POST, DELETE /v1/webhook-endpoints | Register client callback URLs |

**Inbound webhook pipeline (Meta to us).** One endpoint receives events for every tenant. It verifies the X-Hub-Signature-256 signature, acknowledges with HTTP 200 quickly, queues the raw event, then a worker looks up the tenant by phone number ID or WABA ID and processes it. Events are de-duplicated by Meta message ID.

Fields we subscribe to: messages (inbound messages and status updates), message\_template\_status\_update, message\_template\_quality\_update, phone\_number\_quality\_update, phone\_number\_name\_update, account\_update, business\_capability\_update and security.

**Outbound webhooks (us to clients).** We forward normalised events (message.received, message.status, template.status, number.quality) to each tenant's registered URLs. Each request is signed with a per-endpoint secret, retried with exponential backoff for 24 hours, and shown in the delivery log (D8).

## Non-functional requirements

Security and tenant isolation come first, because one leaked BISU token exposes a client's whole WhatsApp account.

| Area | Requirement |
| --- | --- |
| Token security | BISU tokens and App Secret encrypted at rest with a managed KMS key; never sent to the browser or returned by the API; access logged |
| Tenant isolation | Every query scoped by tenant ID; automated tests prove one tenant cannot read another's data |
| Authentication | Passwords hashed (Argon2 or bcrypt), optional TOTP 2FA, session expiry, API keys stored hashed and shown once |
| Webhook integrity | Signature verified on every Meta request; replay protection by message ID; client webhooks signed |
| Privacy | Privacy policy and terms published; data deletion request handling as Meta requires; configurable message retention per tenant; data processing agreement for clients |
| Compliance | Follows WhatsApp Business Messaging Policy and Commerce Policy; opt-in records kept; India's Digital Personal Data Protection Act, 2023 applies to client and customer data; host in an Indian cloud region (for example AWS Mumbai) |
| Scale | Receiver handles bursts of 1,000 events per second at launch; queue-based so a slow worker never drops a Meta event |
| Availability | 99.9% monthly for API and webhook receiver; health checks and alerts on Meta API error rates |
| Observability | Structured logs with tenant and request IDs, metrics per tenant, alerting on token revocation and quality drops |
| Backups | Daily database backups, 30-day retention, tested restore |
| Usability | Responsive dashboard, works on current Chrome, Safari, Edge and Firefox; English plus Indian languages in the dashboard (first languages to be chosen), and full Indian-script support in messages, templates and bots |

## App Review submission plan

We submit only when the dashboard is live on our production domain and both demo videos show real traffic, not mock-ups.

1. Complete Business Verification for our company in Meta Business Suite, using the same legal name as our website footer.
2. Publish the website, privacy policy, terms and data deletion page on the production domain.
3. In the Meta app, fill in app settings (icon, category, privacy and terms URLs, contact email) and add the production domain to the Facebook Login for Business allowed domains.
4. Record Demo 1 (messaging): log in to our dashboard, open the inbox, type a message, press send, and show it arriving on a real WhatsApp phone or WhatsApp Web, with the reply appearing back in the inbox.
5. Record Demo 2 (management): open the template manager, create a utility template with a variable, submit it, and show its pending status in our UI and in WhatsApp Manager.
6. Optionally record Embedded Signup end to end (Connect WhatsApp through to Connected) to show the onboarding a client sees.
7. Write the use-case description for each permission: who our clients are, why we need the permission, and which screen uses it. Add English captions or voice-over to each video.
8. Request Advanced Access for whatsapp\_business\_messaging and whatsapp\_business\_management, and submit.
9. After approval, complete Access Verification so the onboarding cap rises from 10 to 200 clients per 7 days.

Common rejection causes to avoid: videos that show API calls in Postman instead of our UI, a template that is never submitted, a message that is not shown arriving on a phone, and a business name that does not match the verified entity.

## Roadmap, risks and assumptions

_Roadmap diagram: see the live doc at https://claude.ai/code/artifact/6b2a2052-5d7b-4249-8b6e-ce823c418ddb_

Phase 2 is the critical path: App Review cannot be submitted until Embedded Signup, the inbox and the template manager run on our production domain.

**Risks**

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Business Verification rejected (name or domain mismatch) | Blocks everything | Align legal name across documents, website and Meta Business Suite before applying |
| App Review rejected | Delays launch by each resubmission cycle | Follow the submission plan exactly; real phone in Demo 1; template actually submitted in Demo 2 |
| Meta changes Embedded Signup or pricing again | Rework of onboarding or billing screens | Build on v4 only; keep Meta calls behind one internal adapter; track Meta's changelog monthly |
| A client's number is flagged or quality drops | Client messaging limit cut or number blocked | Show quality alerts, enforce opt-in, throttle campaigns within limits |
| Token leak or cross-tenant data access | Severe trust and legal damage | KMS encryption, tenant-scoped queries with tests, audit logs, penetration test before launch |
| Onboarding cap of 10 per week before Access Verification | Slow early sales | Complete Access Verification immediately after App Review |

**Assumptions**

- We launch as a Tech Provider and clients pay Meta directly until Meta approves us as a Solution Partner.
- Cloud API only (Meta-hosted); no on-premises API.
- One Meta app serves all tenants, with one webhook endpoint.
- Release 1 targets web desktop; a mobile app for agents is later.

**Solution Partner track (Phase 2)**

We prepare to become a Solution Partner in Phase 2, alongside the App Review build, so that we can bill clients for Meta fees once Meta accepts us. Until then the platform runs in Tech Provider mode.

- [ ] Apply to Meta's Business Partner program as a WhatsApp Solution Partner once Business Verification is complete; confirm current entry criteria with Meta.
- [ ] Billing data model per tenant: Meta fee usage by category and country, our subscription fees, invoices and payments.
- [ ] Credit line sharing step after Embedded Signup, switched off until Solution Partner approval.
- [ ] Client invoicing module: monthly invoice combining Meta fees and our subscription, with a payment gateway for our target market.
- [ ] Daily reconciliation of our usage records against Meta's billing data.
- [ ] Per-tenant switch between paying Meta directly and paying through us, for clients onboarded before approval.

Open point: Meta controls Solution Partner admission and may expect a track record of client volume first, so live reselling may move to Phase 3 or 4.

**Chatbot and automation track (Phase 2)**

The chatbot builder, WhatsApp Flows designer and AI agents are built in Phase 2, but the App Review submission does not wait for them: Meta reviews only messaging and templates.

- [ ] Chatbot builder: visual flow of triggers (keyword, first message, button reply), messages, conditions and handoff to a human agent in the inbox.
- [ ] WhatsApp Flows designer: create, preview and publish Flows (forms such as lead capture or booking) through Meta's Flows API, and store submitted answers against the contact.
- [ ] AI agent: answers from a tenant's knowledge base (FAQs, documents, website), with confidence threshold and human handoff.
- [ ] Guardrails: bots reply only inside the 24-hour window or with approved templates, honour opt-outs, and log every automated message.
- [ ] API: endpoints to start, pause and assign bots per number, and webhook events for bot handoffs and Flow submissions.
- [ ] Usage limits per plan for AI replies, since each reply has a model cost on top of Meta's message fee.

Open point: which AI model provider to use and whether AI replies are a paid add-on.

**Coexistence track (Phase 1)**

In Phase 1, clients who already use the WhatsApp Business App can connect the same number to our platform and keep using the phone app, through Embedded Signup's Business App onboarding option. Because coexistence runs through Embedded Signup, Phase 1 now also starts the Embedded Signup integration rather than leaving it all to Phase 2.

- [ ] Second onboarding path in Connect WhatsApp (D2) for existing Business App numbers, alongside the standard new-number path.
- [ ] Import of contacts and recent chat history that Meta shares during onboarding.
- [ ] Inbox shows messages sent from the phone app as well as from our dashboard, using Meta's message echo webhooks, so agents see the full conversation.
- [ ] Clear notice to the client of the features and limits that differ for coexistence numbers, checked against Meta's current documentation before build.
- [ ] Analytics separate phone-app messages from API messages for cost estimates.

## Open questions

These answers change scope or design; defaults in brackets are what this BRD assumes until you decide.

- [ ] What is the registered legal name and country of the company applying to Meta, and is it already verified in Meta Business Suite? \[Not yet verified\] Answered: Ecogo AI Technologies Pvt Ltd. Still to confirm: country (Pvt Ltd suggests India) and whether it is already verified in Meta Business Suite.
- [ ] Which production domain will host the dashboard and website? \[To be chosen\] Not decided yet. Needed by Phase 1: Business Verification checks the website on this domain, and Embedded Signup only runs on domains on the allowed list. Answered (may still change): dashboard on whatsapp.ecogo.co.in, added to the Embedded Signup allowed domains. The company website on ecogo.co.in should show the legal name Ecogo AI Technologies Pvt Ltd, privacy policy and terms for Business Verification. Changed 4 Oct 2026: dashboard moved to connect.ecogo.ai (api.connect.ecogo.ai, hooks.connect.ecogo.ai); the whatsapp.ecogo.co.in names stay served during the move.
- [ ] Who are the first target customers (industry, size, country)? This sets pricing, languages and data residency. \[Small and mid-size businesses, English-speaking markets\] Answered: small and mid-size businesses in Indian language-speaking markets. Still open: which industries, and which Indian languages first.
- [ ] How will we charge clients: monthly subscription per number, per seat, or usage-based? \[Monthly subscription per connected number\] Recommended: tiered monthly plans per connected number (for example Starter, Growth, Pro), each including a set number of agent seats and features, with extra seats and Phase 2 add-ons (chatbot, AI replies) charged separately. No per-message fee from us at launch; clients see Meta's message fees on their own Meta bill. Prices in INR still to set.
- [ ] Preferred tech stack and hosting? \[Node.js or TypeScript backend, PostgreSQL, Redis queue, React dashboard, on AWS\] Answered: Go backend, React dashboard, PostgreSQL, hosted on AWS (Mumbai region). Still to choose: the webhook queue (Amazon SQS or Redis).
- [ ] Do we want WhatsApp Business App coexistence (clients keep using the phone app) in release 1? \[Planned in Phase 1\] Answered: yes, coexistence is in release 1, Phase 1.
- [ ] Is a chatbot or AI auto-reply needed for launch? \[Yes, planned in Phase 2\] Answered: confirmed for Phase 2.
- [ ] Will we later apply to be a Solution Partner and bill Meta fees ourselves? \[Yes, planned in Phase 2\]
- [ ] Who on the team will own Meta communication and the App Review submission? \[Muhammed Arshad\]

## Sources

Meta's developer site could not be opened from this workspace, so the Meta requirements above come from Meta documentation pages as indexed by search plus partner guides. Confirm the onboarding cap, pricing and deprecation date against Meta's pages before submission.

- [Become a Tech Provider (Meta)](https://developers.facebook.com/documentation/business-messaging/whatsapp/solution-providers/get-started-for-tech-providers)
- [Embedded Signup overview (Meta)](https://developers.facebook.com/documentation/business-messaging/whatsapp/embedded-signup/overview/)
- [WhatsApp webhooks (Meta)](https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/overview/)
- [WhatsApp changelog (Meta)](https://developers.facebook.com/documentation/business-messaging/whatsapp/changelog)
- [Tech Provider Program integration guide (Infobip)](https://www.infobip.com/docs/whatsapp/tech-provider-program/setup-and-integration)
- [Tech Provider business onboarding limits (Infobip)](https://www.infobip.com/docs/whatsapp/tech-provider-program/business-onboarding)
- [Pricing update effective 1 Oct 2026 (YCloud)](https://www.ycloud.com/blog/whatsapp-api-message-pricing-update-effective-october-1-2026)
- [Business portfolio messaging limits (Alibaba Cloud)](https://www.alibabacloud.com/help/en/chatapp/notice-on-changes-to-whatsapp-messaging-limits)
- Owner's brief: Building an independent integration (project file)
