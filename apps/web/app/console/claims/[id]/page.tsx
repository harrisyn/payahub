import Link from "next/link";
import { notFound } from "next/navigation";

import { api } from "@/lib/api";
import { dateTime, money, stateLabel, stateTone } from "@/lib/format";
import type { ClaimEvent, ClaimState } from "@/lib/types";

import { Refresh } from "./refresh";

export const metadata = { title: "Claim" };

type Claim = {
  id: string;
  environment: string;
  state: ClaimState;
  payer_id: string;
  payer_claim_number?: string;
  currency: string;
  total: number;
  member: { member_number: string };
  provider: { name: string };
  encounter: { type: string; start_date: string };
  lines: { id: string; description: string; quantity: number; unit_price: number }[];
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
  returned_to_ready: "Not sent; back to ready",
  validation_failed: "Stopped before sending: fix needed",
};

export default async function ClaimPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const [c, ev] = await Promise.all([
    api<Claim>(`/v1/claims/${encodeURIComponent(id)}`),
    api<{ data: ClaimEvent[] }>(`/v1/claims/${encodeURIComponent(id)}/events`),
  ]);
  if (!c.ok) notFound();
  const claim = c.data;
  const events = ev.ok ? ev.data.data : [];
  const moving = claim.state === "ready" || claim.state === "submitted";

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
            <span className={`app-pill app-pill--${stateTone(claim.state)}`}>{stateLabel[claim.state]}</span>{" "}
            {claim.payer_claim_number ? `Insurer's number ${claim.payer_claim_number}.` : moving ? "Updating as it moves." : ""}
          </p>
        </div>
      </div>
      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="trail-h">
          <h2 id="trail-h" className="app-card__title">
            What happened
          </h2>
          <ol className="app-trail">
            {events.map((e) => {
              const err = (e.detail as { error?: { message?: string; payer_message?: string } } | undefined)?.error;
              return (
                <li key={e.id} className={`app-trail__item app-trail__item--${e.kind}`}>
                  <span className="app-trail__when num">{dateTime(e.created_at)}</span>
                  <span>
                    <span className="app-step__title">{eventText[e.kind] ?? e.kind}</span>
                    {err && <span className="app-muted app-block">{err.payer_message || err.message}</span>}
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
              <dd>{claim.diagnoses.map((d) => `${d.code.value} (${d.role})`).join(", ")}</dd>
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
                  <td>{l.description}</td>
                  <td className="num">{l.quantity}</td>
                  <td className="num">{money(l.unit_price * l.quantity, claim.currency)}</td>
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
