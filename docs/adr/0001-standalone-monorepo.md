# 1. Standalone monorepo, separate from Serenity

Date: 2026-10-22 · Status: accepted

## Context

Claim submission lives inside serenity-health/core-api-v2 today: five payer
adapters, a unified claim submission service and an AI-assisted code mapping
pipeline. It is built around Serenity's visits, charge items and admissions,
so no other organisation can use it.

## Decision

Payahub is its own product in its own repo. It has its own canonical claim
model and a public REST API that every channel uses, including Serenity. The
backend stays in Python so the existing adapters port with little rewriting;
the marketing site, onboarding, dashboard and docs are one Next.js app.

## Consequences

- Serenity becomes Payahub's first customer, moved one payer at a time behind
  a switch (shadow mode first), and its in-house adapters are retired after.
- Payer knowledge lives in connector manifests and data, not in engine code.
