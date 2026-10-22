# 2. AI proposes, rules decide; bill on acknowledgement

Date: 2026-10-22 · Status: accepted

## Decision

- No model output is ever sent to a payer. Connectors assemble every payload
  deterministically from validated claim fields.
- Every AI-set field carries Evidence: origin, confidence, model and prompt
  version, prompt hash, and a plain-words assertion of what it means.
- Confidence thresholds are per use (extraction, code mapping), never one
  global number. Below threshold the claim goes to NEEDS_REVIEW and a person
  confirms it.
- The billable event is a claim the payer acknowledged (ACKNOWLEDGED, APPROVED
  or PAID). Drafts, dry runs, rejections at intake and resubmissions of the
  same claim are not billed.

## Why

These are the guardrails in the claims-integrator plan: a plausible wrong
mapping submitted at scale is the failure that kills this business, and a
payer audit must be able to see why each value was sent.
