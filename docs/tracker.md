# Build tracker

What the plan (`claims-integrator-plan.md`) and the owner's asks need, and where each item stands.
Update it with every PR. `[x]` done and tested, `[~]` partly done, `[ ]` not started.

The goal: any clinic, however it integrates (API, spreadsheet, scanned forms, web form), gets every bill
to the right payer in the payer's own codes, correctly, with nothing lost and nothing sent on a guess.

## 1. Payers and provisioning

- [x] One shared payer catalogue, each payer standalone or behind a platform (ADR in PR #2)
- [x] A clinic picks a payer and adds its platform account once; credentials sealed, write-only
- [ ] The real payer list: every payer Serenity reaches today, its platform and its insurer code
      (blocked: needs read access to Serenity's database)
- [x] Operator console: add and edit payers, their platform fields and code lists without a deploy
      (catalogue in the database, seeded once from the JSON files; every process refreshes within 5s;
      change history per payer; `payahub operator EMAIL` grants the role)
- [x] Payer code-list import (CSV) with coverage per kind, matches to confirm, and a check that every
      saved code is in the payer's list
- [x] Matching refuses names that share only a strength, form or sort ("Cetirizine 10 mg" is not "Amlodipine 10mg")

## 2. Services, medicines and diagnoses

- [x] Standard item list (services, labs, imaging, procedures, medicines with ATC, consumables)
- [x] A clinic maps its own codes to standard items once; suggestions to confirm
- [~] Line resolution: clinic code → standard item → payer code, at create and at send
- [x] Clinic price list: add, remove, import CSV or Excel (merge or replace, dry run, skipped rows listed);
      a claim line with the clinic's code needs no price or name; each code gets a suggested standard item to confirm
- [x] A larger standard list for Ghana: 262 items (consultations and ward days, 43 labs, 24 imaging,
      20 procedures, 124 medicines with ATC codes, 20 consumables), each found first by its name and every synonym
- [x] Diagnoses: ICD-10 list (ICD-10-CM plus WHO-only codes in use in Ghana), search, and checks on every claim
- [ ] Payer diagnosis codes where a payer uses its own; the NHIA medicines list once we have the file
- [ ] Refresh the ICD-10 list to the latest release (the shipped copy predates 2020; WHO COVID codes are added by hand)
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
- [ ] Member check and OTP in the console and the API
- [ ] Claim status from the payer after sending (accepted, queried, paid, rejected with reason)

## 4. AI translation and matching

- [x] Rule: no model output reaches a payer; AI-set fields carry evidence; below threshold goes to review
- [~] Code suggestion: word matching today, no model
- [ ] Model-backed matching (clinic item → standard item, standard → payer code, diagnosis text → ICD-10)
      with evidence, per-use thresholds, and the model and prompt version logged on every decision
- [ ] Review queue in the console for low-confidence lines
- [ ] Regression checks: a changed input must change the output; known pairs must keep their answer
- [ ] Intake: spreadsheet upload of claims
- [ ] Intake: PDF and photo of a paper claim form, extracted then reviewed

## 5. Checks before sending

- [x] Required fields, unmapped lines, totals
- [ ] Payer rules as data (allowed items per payer, quantity limits, prices)
- [ ] Duplicate claim detection
- [ ] Filing deadline (60 days, LI 1809)

## 6. Platform

- [x] Durable queue, a claim and its job commit together; reconcile before resend
- [x] Idempotency-Key on every write; audit trail
- [x] Landing page, sign-up, trial, onboarding console, API docs
- [x] Brand: Ananse exchange logo and palette (PR #3)
- [x] Standard form kit (`@/components/ui`, Mantine 9): searchable selects, remote type-ahead through
      `/lookup/[kind]`, prepend/append inputs, money in minor units, rich text, tags, dates; shown at `/ui`
- [ ] Move every existing console form onto the kit (native selects and inputs today)
- [x] Landing page redesign: animated claims-flow hero, how-it-works visuals, live sandbox, share image,
      `NEXT_PUBLIC_SITE_INDEXABLE` switch for launch
- [x] Landing page taste pass, problem story, platform layer and integration-guide sections, SEO structured data
- [x] Lead capture on the landing page (contact me, platform not listed, do you have this payer, interface specs):
  `POST /v1/leads` (public, idempotent), operator Enquiries page with most-wanted payers, `/openapi.yaml` served from the API
- [ ] Notify operators of a new enquiry by email once outbound email is set up
- [ ] Team members and roles
- [ ] Webhooks to the clinic's system on every state change
- [ ] Billing and usage (Paystack)
- [ ] Throughput test at scale on the full path

## Waiting on the owner

- Read access to Serenity's database, for the real payer list and mappings
- Test accounts on XCeli, Medserve, SmartClaims, RX Insurance and ClaimIQ
- An AI provider key, and agreement on which provider sees claim data
- Final name, prices and contact email
