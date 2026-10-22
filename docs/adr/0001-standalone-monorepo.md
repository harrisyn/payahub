# 1. Standalone product, Go backend

Date: 2026-10-22 · Status: accepted (revised the same day: Python replaced by Go)

## Context

Claim submission lives inside serenity-health/core-api-v2 today, built
around Serenity's visits, charge items and admissions, so no other
organisation can use it. Payahub is mostly headless: integrators call the
API, and the web UI exists to configure organisations, payers and
credentials. It must stay fast at millions of claims and never lose one.

## Decision

- Payahub is its own product in its own repo, with its own canonical claim
  model and a public REST API that every channel uses, including Serenity.
- The backend is **Go**, one static binary (`payahub api | worker | migrate`)
  on `net/http` with no web framework. Dependencies are kept to pgx (Postgres),
  River (queue) and uuid.
- The web app (landing page, onboarding, configuration, docs) is Next.js.

Go over Python because the work is I/O-bound fan-out to payers with heavy
concurrency: goroutines give thousands of in-flight payer calls per process
at a few KB each, there is no interpreter or GIL, and deployment is one small
binary. AI steps call model APIs over HTTP, so they do not need Python.

## Consequences

- The five core-api-v2 adapters are ported to Go rather than copied; their
  documented payer quirks carry over, their untyped interface does not.
- Serenity becomes Payahub's first customer, moved one payer at a time
  behind a switch (shadow mode first), and its in-house adapters are retired.
