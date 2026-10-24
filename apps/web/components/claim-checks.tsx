"use client";

import Link from "next/link";
import { useActionState } from "react";

import {
  confirmIssues,
  sendClaim,
  type FormState,
  type SendState,
} from "@/app/actions";
import { Messages, Submit } from "@/components/forms";

export type Issue = {
  code: string;
  severity: "error" | "warning" | "review";
  field?: string;
  message: string;
};

// What a duplicate's field names: "claim:<id>".
function other(field?: string) {
  return field?.startsWith("claim:") ? field.slice(6) : null;
}

function Text({ i }: { i: Issue }) {
  const dup = other(i.field);
  return (
    <>
      {i.message}
      {dup && (
        <>
          {" "}
          <Link href={`/console/claims/${dup}`}>See the other claim</Link>
        </>
      )}
    </>
  );
}

// The console says "insurer" where the API says "payer".
const said: Record<string, string> = {
  acknowledged: "The insurer accepted the claim.",
  approved: "The insurer accepted the claim.",
  paid: "The insurer accepted the claim.",
  rejected: "The insurer rejected the claim.",
};

// ClaimChecks shows what stands between a claim and its insurer, lets a
// person confirm what awaits review, and sends the claim, waiting for the
// insurer's answer.
export function ClaimChecks({
  id,
  issues,
  sendable,
  resend,
  sendKey,
}: {
  id: string;
  issues: Issue[];
  sendable: boolean;
  resend: boolean;
  sendKey: string;
}) {
  const [confirmed, confirmAction] = useActionState(
    confirmIssues,
    {} as FormState,
  );
  const [sent, sendAction] = useActionState(sendClaim, {} as SendState);
  const errors = issues.filter((i) => i.severity === "error");
  const review = issues.filter((i) => i.severity === "review");
  const warnings = issues.filter((i) => i.severity === "warning");
  const blocked = errors.length > 0 || review.length > 0;
  const o = sent.outcome;
  if (!sendable && !o && !sent.ok && !sent.error) return null;

  return (
    <section className="app-card app-checks" aria-labelledby="checks-h">
      <h2 id="checks-h" className="app-card__title">
        {o ? "The insurer’s answer" : "Before it goes"}
      </h2>
      {o ? (
        <p
          className={`app-banner${o.state === "rejected" || o.state === "ready" ? " app-banner--bad" : o.final ? " app-banner--good" : ""}`}
          role="status"
        >
          <span>
            {said[o.state] ?? o.message}
            {o.payer_claim_number && (
              <> Insurer&rsquo;s number {o.payer_claim_number}.</>
            )}
            {o.error && (
              <span className="app-block">
                Reason: {o.error.payer_message || o.error.message}
              </span>
            )}
          </span>
        </p>
      ) : (
        <p
          className={`app-banner${blocked ? "" : " app-banner--good"}`}
          role="status"
        >
          {errors.length > 0
            ? `${errors.length} ${errors.length === 1 ? "thing stops" : "things stop"} this claim. Fix ${errors.length === 1 ? "it" : "them"} at the source and send it again.`
            : review.length > 0
              ? `${review.length} ${review.length === 1 ? "thing needs" : "things need"} a person to check before it can go.`
              : "Nothing stops this claim. It is ready to send."}
        </p>
      )}

      {errors.length > 0 && (
        <ul className="app-issues" aria-label="To fix">
          {errors.map((i, n) => (
            <li key={n} className="app-issue app-issue--error">
              <span className="app-issue__tag">Fix</span>
              <span>
                <Text i={i} />
              </span>
            </li>
          ))}
        </ul>
      )}

      {review.length > 0 && (
        <form action={confirmAction} className="app-form app-review">
          <input type="hidden" name="id" value={id} />
          <fieldset className="app-fieldset">
            <legend>Check these against the source</legend>
            {review.map((i, n) => (
              <label key={n} className="app-issue app-issue--review">
                <input
                  type="checkbox"
                  name="confirm"
                  value={`${i.code}|${i.field ?? ""}`}
                />
                <span>
                  <Text i={i} />
                </span>
              </label>
            ))}
          </fieldset>
          <div className="field">
            <label htmlFor="c-note">Note (optional)</label>
            <input
              id="c-note"
              name="note"
              maxLength={500}
              placeholder="What you checked, e.g. a second visit the same day"
            />
          </div>
          <div className="app-form__foot">
            <Submit className="app-button">Confirm what I checked</Submit>
            <Messages state={confirmed} />
          </div>
        </form>
      )}

      {warnings.length > 0 && (
        <ul className="app-issues" aria-label="Worth knowing">
          {warnings.map((i, n) => (
            <li key={n} className="app-issue app-issue--warning">
              <span className="app-issue__tag">Note</span>
              <span>
                <Text i={i} />
              </span>
            </li>
          ))}
        </ul>
      )}

      {sendable && !o && (
        <form action={sendAction} className="app-form__foot app-checks__send">
          <input type="hidden" name="id" value={id} />
          <input type="hidden" name="key" value={sendKey} />
          {blocked ? (
            <button
              type="submit"
              className="app-button app-button--primary"
              disabled
            >
              {resend ? "Send again" : "Send to the insurer"}
            </button>
          ) : (
            <Submit className="app-button app-button--primary">
              {resend ? "Send again" : "Send to the insurer"}
            </Submit>
          )}
          <span className="app-muted">
            You see the insurer&rsquo;s answer here, usually within seconds.
          </span>
          <Messages state={sent} />
        </form>
      )}
    </section>
  );
}
