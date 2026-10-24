# Build tracker

What the plan (`claims-integrator-plan.md`) and the owner's asks need, and where each item stands.
Update it with every PR. `[x]` done and tested, `[~]` partly done, `[ ]` not started.

The goal: any clinic, however it integrates (API, spreadsheet, scanned forms, web form), gets every bill
to the right payer in the payer's own codes, correctly, with nothing lost and nothing sent on a guess.

## 1. Payers and provisioning

- [x] One shared payer catalogue, each payer standalone or behind a platform (ADR in PR #2)
- [x] A clinic picks a payer and adds its platform account once; credentials sealed, write-only
- [x] Payer import: operators load an export of Serenity's payers (CSV or Excel), check what each row
      will do, then import; existing settings are never overwritten (export query in docs/serenity-payers.md)
- [ ] Run the export on Serenity's database and import the real payer list (needs someone with DB access)
- [x] Operator console: add and edit payers, their platform fields and code lists without a deploy
      (catalogue in the database, seeded once from the JSON files; every process refreshes within 5s;
      change history per payer; `payahub operator EMAIL` grants the role)
- [x] Payer code-list import (CSV) with coverage per kind, matches to confirm, and a check that every
      saved code is in the payer's list
- [x] Matching refuses names that share only a strength, form or sort ("Cetirizine 10 mg" is not "Amlodipine 10mg")

## 2. Services, medicines and diagnoses

- [x] Standard item list (services, labs, imaging, procedures, medicines with ATC, consumables)
- [x] A clinic maps its own codes to standard items once; suggestions to confirm
- [x] Line resolution: clinic code → standard item → payer code, at create, at send and again when a code is mapped
- [x] Clinic price list: add, remove, import CSV or Excel (merge or replace, dry run, skipped rows listed);
      a claim line with the clinic's code needs no price or name; each code gets a suggested standard item to confirm
- [x] A larger standard list for Ghana: 262 items (consultations and ward days, 43 labs, 24 imaging,
      20 procedures, 124 medicines with ATC codes, 20 consumables), each found first by its name and every synonym
- [x] Diagnoses: ICD-10 list (ICD-10-CM plus WHO-only codes in use in Ghana), search, and checks on every claim
- [ ] Payer diagnosis codes where a payer uses its own; the NHIA medicines list once we have the file
- [x] ICD-10 list refreshed to the ICD-10-CM 2026 release (1 April 2026): 98,186 codes, 2,564 more than before
      (COVID-19 and post-COVID codes among them); `go run ./internal/icd10/gen` rebuilds it from the next release
- [x] Claims carry prescriptions on drug lines and provisional/confirmed status on diagnoses

## 3. Connectors that really call the platforms

Ported from core-api-v2 `backend/app/plugins/insurance/`, keeping every payer quirk its comments document,
each with contract tests against a recorded copy of the platform's API.

- [x] Sandbox (direct) and Sandbox Exchange (platform)
- [x] Line-by-line sending with a progress journal: resume after a crash, adopt a line whose reply was lost,
      bring a corrected claim in line (update, delete, add) instead of opening it twice, reopen an expired claim
- [x] XCeli (11 contract tests)
- [x] Medserve (14: package and coverage lookup, approvals, cover exhausted, lost replies)
- [x] SmartClaims (13: CGS rule sheet, rollback of a refused item, prescriptions, no data leaks)
- [x] RX Insurance (8: 012/013, expired claims reopened with a new HMS number, no benefit, authorisation)
- [x] ClaimIQ (7: whole claim plus batch of one, line by line, idempotent retries)
- [ ] Live check of each against the real platform (blocked: needs test accounts)
- [x] Member check and OTP in the console and the API: `POST /v1/member-checks` waits for the answer; the code is sealed on its way to the worker
- [x] Claim status from the payer after sending: followed until paid or rejected (2h, 6h, 12h, then daily for 120 days), each change an event and webhook; `POST /v1/claims/{id}/status-check` asks now

## 4. AI translation and matching

- [x] Rule: no model output reaches a payer; AI-set fields carry evidence; below threshold goes to review
- [x] Model providers: Claude (Messages API, structured output, effort, server-side fallback), OpenAI
      and any OpenAI-compatible endpoint; each task tries its providers in order and fails over on
      outages or refusals; keys sealed in the vault, set and checked by operators in the console
- [x] Model-backed matching of clinic items to standard items: the model can only pick a catalogue item of
      the right kind; word matching fills in and takes over when no model answers; each pick carries its
      reason, model, prompt version and hash
- [x] Every model call recorded (task, model, prompt version and hash, input hash, tokens, time, outcome);
      30-day use per task and model on the AI models page
- [x] Model-backed standard item → payer code (operators: Ask the model on a payer's codes; the model picks only
      from the payer's own list, a pick of another strength or form is never sure, picks wait to be accepted) and
      diagnosis text → ICD-10 (`POST /v1/diagnoses/suggest`, Code with the model under Services and medicines, and
      a form's diagnosis written without a code; only codes in the list, words scrubbed first)
- [x] Review queue (Claims, To review; `GET /v1/review-queue`): own codes holding unsent claims back, mapped once to
      recheck every claim waiting on them; model picks under the threshold and possible copies, confirmed with a note
- [x] Regression checks: 63 clinic names with known answers (15 groups that differ only in strength or form
      must each get a different item); run on word matching in every test run and on the model from AI models, with history
- [x] Intake: spreadsheet upload of claims (CSV or Excel, one row per line, checked before saving,
      re-uploads create nothing new, ready claims can be sent straight away; template in the console)
- [x] Intake: PDF and photo of a paper claim form, read by a model, every value checked (dates, amounts,
      ICD-10, insurer, standard items from the catalogue), fields below 90% marked with the words they came
      from; a person checks and saves (console: Claims, Read a claim form; API: POST /v1/claims/extract)
- [ ] Try the reading on real Ghanaian claim forms with Claude and set the thresholds from the results

## 5. Checks before sending

- [x] Required fields, unmapped lines, totals
- [x] Payer rules as data: each code in an insurer's list can carry most per claim, one sex, an age range and
      authorisation (columns in the list CSV); a price above the tariff or a code outside the list warns or stops, per insurer
- [x] Duplicate claim detection: the same patient, payer and visit date; an exact copy waits for a person to confirm
- [x] Filing deadline (60 days, LI 1809, per payer), warning in the last week; a payer can be set to block late claims
- [x] Confirming issues that await review, recorded with who and why (`POST /v1/claims/{id}/review`)
- [x] Claim page: the checks (to fix, to confirm, worth knowing), confirm with a note, and send with the insurer's answer shown on the page

## 6. Platform

- [x] Durable queue, a claim and its job commit together; reconcile before resend
- [x] Idempotency-Key on every write; audit trail
- [x] Landing page, sign-up, trial, onboarding console, API docs
- [x] Brand: Ananse exchange logo and palette (PR #3)
- [x] Standard form kit (`@/components/ui`, Mantine 9): searchable selects, remote type-ahead through
      `/lookup/[kind]`, prepend/append inputs, money in minor units, rich text, tags, dates; shown at `/ui`
- [x] Every console and sign-in form on the kit: all selects searchable; native fields left only for files, radio cards and checkboxes
- [x] Landing page redesign: animated claims-flow hero, how-it-works visuals, live sandbox, share image,
      `NEXT_PUBLIC_SITE_INDEXABLE` switch for launch
- [x] Landing page taste pass, problem story, platform layer and integration-guide sections, SEO structured data
- [x] Lead capture on the landing page (contact me, platform not listed, do you have this payer, interface specs):
  `POST /v1/leads` (public, idempotent), operator Enquiries page with most-wanted payers, `/openapi.yaml` served from the API
- [x] Outbound email (SMTP, queued in the same transaction as its cause, retried); operators are emailed every new enquiry once
- [x] Docker: `docker-compose.yml` runs the whole product locally (web, API and worker, Postgres, Mailpit for email);
      `docker-compose.prod.yml` deploys to Dokploy (migrate before every release, API with healthcheck, scalable
      workers with graceful stop, web on the private network, Postgres on a named volume for backups);
      steps in `docs/deploy.md`; CI builds both images
- [ ] First production deploy on Dokploy: DNS for the web and API hostnames, secrets set, vault key stored apart, volume backups on
- [ ] Set PAYAHUB_SMTP_URL and PAYAHUB_MAIL_FROM in production (needs a mail account, e.g. a transactional provider)
- [x] Team members and roles: owner, admin, claims, finance, developer, viewer; email invitations; every console write checked against the role (a new route is closed until given a permission)
- [x] Real-time answers: create or submit with `Prefer: wait=N` and get the payer's answer in the same response (202 if it takes longer); the async path stays
- [x] Remove patient identity before every AI call: a provider refuses any text or file the de-identifier did not produce; forms are blacked out on our server (Tesseract) and the model sees only those pages; what was removed is kept on each AI run
- [ ] Try the redaction on real Ghanaian forms (handwritten, photographed at an angle) and widen the labels it knows
- [x] Webhooks to the clinic's system on every state change: written with the event in one transaction, signed (Standard Webhooks), retried for about 2.5 days, delivery log and test sends in the console
- [x] Wallets: double-entry ledger; organisation and platform wallets; per-claim charges on acknowledgement and
      monthly fees from the wallet; live claims pause when the wallet does not cover one more
- [x] Payment providers behind one interface: Paystack (card, mobile money, bank payouts) and a sandbox;
      fundings credited only from the provider's verification; withdrawals approved by an operator, then paid
- [ ] Paystack live keys and webhook URL set in production; first real funding checked end to end
- [ ] A second payment provider (e.g. Hubtel or Flutterwave) once a customer needs it
- [x] Throughput test at scale on the full path (`PAYAHUB_LOAD=20000 go test -run TestThroughput -v ./internal/api/`):
      20,000 claims of 3 lines in the clinic's own codes, resolved, checked, queued, sent to the sandbox payer and
      acknowledged, every step a signed webhook, 1 in 10 waiting for the answer. On one 4-core machine running
      Postgres, the API and the load together: about 480 claims a second (1.7 million an hour), create and submit
      p50 156 ms and p99 346 ms, the payer's answer p50 626 ms, webhooks within 3 s of the last claim. Fixed on the
      way: a save notified on every commit, which takes one database-wide lock (now only when someone waits), and
      webhook results were committed one at a time (now once per batch). The limit now is the disk's commit flush

## Waiting on the owner

- Read access to Serenity's database, for the real payer list and mappings
- Test accounts on XCeli, Medserve, SmartClaims, RX Insurance and ClaimIQ
- An AI provider key (entered in the console under AI models), and agreement on which provider sees claim data
- Final name, prices and contact email
- The Dokploy server and the production hostnames (web and API)
