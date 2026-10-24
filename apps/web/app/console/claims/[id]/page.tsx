import Link from "next/link";
import { notFound } from "next/navigation";

import { api } from "@/lib/api";
import { dateTime, money, stateLabel, stateTone } from "@/lib/format";
import type { ClaimEvent, ClaimState } from "@/lib/types";

import { ClaimChecks, type Issue } from "@/components/claim-checks";
import { WithInsurer } from "@/components/claim-status";
import { can } from "@/lib/roles";
import { requireOrg } from "@/lib/session";

import { Refresh } from "./refresh";

export const metadata = { title: "Claim" };

type Claim = {
  id: string;
  environment: string;
  state: ClaimState;
  payer_id: string;
  payer_claim_number?: string;
  payer_status?: string;
  payer_message?: string;
  status_checked_at?: string;
  currency: string;
  total: number;
  member: { member_number: string };
  provider: { name: string };
  encounter: { type: string; start_date: string };
  lines: {
    id: string;
    description: string;
    quantity: number;
    unit_price: number;
    code?: { system: string; value: string };
    item?: string;
    mapping?: { target?: { value: string } };
  }[];
  diagnoses: { code: { system: string; value: string }; role: string }[];
  created_at: string;
};

const eventText: Record<string, string> = {
  created: "Claim received",
  submission_queued: "Checked and queued to send",
  submission_started: "Sending to the insurer",
  attempt_failed: "Attempt failed; will retry",
  acknowledged: "Insurer acknowledged the claim",
  submitted: "Sent; waiting for the insurer",
  rejected: "Insurer rejected the claim",
  approved: "Insurer approved the claim",
  paid: "Insurer paid the claim",
  queried: "Insurer asked a question",
  returned_to_ready: "Not sent; back to ready",
  validation_failed: "Stopped before sending: fix needed",
  reviewed: "Checked by a person",
};

// Events after which a send is under way: the page follows it.
const underWay = new Set([
  "submission_queued",
  "submission_started",
  "attempt_failed",
  "submitted",
]);

export default async function ClaimPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  const [{ organization: org }, c, ev] = await Promise.all([
    requireOrg(),
    api<Claim>(`/v1/claims/${encodeURIComponent(id)}`),
    api<{ data: ClaimEvent[] }>(`/v1/claims/${encodeURIComponent(id)}/events`),
  ]);
  if (!c.ok) notFound();
  const claim = c.data;
  const events = ev.ok ? ev.data.data : [];
  const last = events.at(-1)?.kind ?? "";
  const moving = claim.state === "submitted" || underWay.has(last);
  // A claim that has not gone, or came back rejected, can be checked and sent.
  const sendable =
    !moving &&
    [
      "draft",
      "mapped",
      "validated",
      "needs_review",
      "ready",
      "rejected",
    ].includes(claim.state);
  const checks = sendable
    ? await api<{ issues: Issue[] }>(
        `/v1/claims/${encodeURIComponent(id)}/checks`,
      )
    : null;

  return (
    <div className="app-page">
      {moving && <Refresh />}
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/claims">Claims</Link>
          </p>
          <h1 className="app-id-title">
            {claim.member.member_number}, {money(claim.total, claim.currency)}
          </h1>
          <p className="app-finding">
            <span className={`app-pill app-pill--${stateTone(claim.state)}`}>
              {stateLabel[claim.state]}
            </span>{" "}
            {claim.payer_claim_number
              ? `Insurer's number ${claim.payer_claim_number}.`
              : moving
                ? "Updating as it moves."
                : ""}
          </p>
        </div>
      </div>
      {/* Always rendered, so the insurer's answer to a send stays on screen when the page refreshes. */}
      <ClaimChecks
        id={claim.id}
        issues={checks?.ok ? checks.data.issues : []}
        sendable={sendable && !!checks?.ok}
        resend={claim.state === "rejected"}
        sendKey={`send-${claim.id}-${events.length}`}
      />
      {(claim.state === "acknowledged" || claim.state === "approved") && (
        <WithInsurer
          id={claim.id}
          status={claim.payer_status}
          message={claim.payer_message}
          checkedAt={claim.status_checked_at}
          canAsk={can(org.user.permissions, "claims")}
        />
      )}
      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="trail-h">
          <h2 id="trail-h" className="app-card__title">
            What happened
          </h2>
          <ol className="app-trail">
            {events.map((e) => {
              const detail = e.detail as
                | {
                    error?: { message?: string; payer_message?: string };
                    payer_message?: string;
                  }
                | undefined;
              const err = detail?.error;
              const said = !err && detail?.payer_message;
              return (
                <li
                  key={e.id}
                  className={`app-trail__item app-trail__item--${e.kind}`}
                >
                  <span className="app-trail__when num">
                    {dateTime(e.created_at)}
                  </span>
                  <span>
                    <span className="app-step__title">
                      {eventText[e.kind] ?? e.kind}
                    </span>
                    {err && (
                      <span className="app-muted app-block">
                        {err.payer_message || err.message}
                      </span>
                    )}
                    {said && (
                      <span className="app-muted app-block">
                        &ldquo;{said}&rdquo;
                      </span>
                    )}
                  </span>
                </li>
              );
            })}
          </ol>
        </section>
        <section className="app-card" aria-labelledby="claim-h">
          <h2 id="claim-h" className="app-card__title">
            The claim
          </h2>
          <dl className="app-dl">
            <div>
              <dt>Claim</dt>
              <dd className="app-id">{claim.id}</dd>
            </div>
            <div>
              <dt>Environment</dt>
              <dd>{claim.environment === "live" ? "Live" : "Sandbox"}</dd>
            </div>
            <div>
              <dt>Provider</dt>
              <dd>{claim.provider.name}</dd>
            </div>
            <div>
              <dt>Visit</dt>
              <dd>
                {claim.encounter.type}, {claim.encounter.start_date}
              </dd>
            </div>
            <div>
              <dt>Diagnoses</dt>
              <dd>
                {claim.diagnoses
                  .map((d) => `${d.code.value} (${d.role})`)
                  .join(", ")}
              </dd>
            </div>
          </dl>
          <table className="app-table">
            <thead>
              <tr>
                <th scope="col">Line</th>
                <th scope="col" className="num">
                  Qty
                </th>
                <th scope="col" className="num">
                  Amount
                </th>
              </tr>
            </thead>
            <tbody>
              {claim.lines.map((l) => (
                <tr key={l.id}>
                  <td>
                    {l.description}
                    <span className="app-muted app-block">
                      {[
                        l.code && l.code.system !== "payahub"
                          ? `Your code ${l.code.value}`
                          : null,
                        l.item ? `standard ${l.item}` : null,
                        l.mapping?.target
                          ? `insurer code ${l.mapping.target.value}`
                          : "no insurer code yet",
                      ]
                        .filter(Boolean)
                        .join(" → ")}
                    </span>
                  </td>
                  <td className="num">{l.quantity}</td>
                  <td className="num">
                    {money(l.unit_price * l.quantity, claim.currency)}
                  </td>
                </tr>
              ))}
              <tr className="app-total">
                <td>Total</td>
                <td />
                <td className="num">{money(claim.total, claim.currency)}</td>
              </tr>
            </tbody>
          </table>
        </section>
      </div>
    </div>
  );
}
