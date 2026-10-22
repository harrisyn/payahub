# 3. Durable submission queue on Postgres (River)

Date: 2026-10-22 · Status: accepted

## Requirement

No accepted claim may be lost, a worker crash must not lose or duplicate a
submission, and the design must scale to millions of claims.

## Decision

Postgres is the system of record, and submission jobs live in the same
database through [River](https://riverqueue.com).

- **Transactional enqueue.** `POST /claims/{id}/submit` validates the claim,
  marks it queued and inserts its job in one transaction. Both commit or
  neither does, so there is no window where a claim is accepted but its job is
  missing (the classic dual-write problem with a separate broker).
- **No transaction held across a payer call.** The worker (1) marks the claim
  submitted and commits, (2) calls the payer, (3) records the outcome and
  completes the job in one transaction.
- **Crash recovery without duplicates.** If a worker dies between (1) and (3),
  River re-runs the job. A retry first asks the payer for the claim by our
  claim ID (`FindClaim`), and only sends again if the payer does not have it.
  Tests cover a timeout before the payer saw the claim and a reply lost after
  it did.
- **Retries.** Transport failures retry with River's backoff, up to 20
  attempts; payer refusals end the job at once. A job that runs out of
  attempts stays in `river_job` as discarded, with its claim still `submitted`
  and queued, so nothing disappears; operations can retry it.
- **Audit.** Every state change and attempt appends to `claim_events`.

## Scaling

Measured on one 4-core machine running the load generator, API, workers and
Postgres together, with full durability (synchronous commit on): 20,000 claims
created, submitted and acknowledged by the sandbox payer in 26 s, about
800 claims/s end to end (5 committed transactions per claim).

API and worker processes are stateless and scale out horizontally; Postgres is
the ceiling. In order, as volume demands: a batch endpoint that inserts many
claims and jobs per transaction; separate queues per payer so one slow payer
cannot starve the rest; time-partitioning of `claims` and `claim_events`; read
replicas for reporting. If one Postgres primary stops being enough, add a
transactional outbox relayed to NATS JetStream or Kafka for fan-out, keeping
Postgres as the source of truth.
