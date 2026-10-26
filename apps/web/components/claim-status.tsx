"use client";

import { useActionState } from "react";

import { askInsurer, type FormState } from "@/app/actions";
import { Messages, Submit } from "@/components/forms";
import { dateTime } from "@/lib/format";

const statusText: Record<string, string> = {
  submitted: "Under review at the insurer.",
  open: "Open at the insurer.",
  approved: "Approved; waiting for payment.",
  queried: "The insurer asked a question.",
  unknown: "The insurer did not say.",
};

// WithInsurer shows where an acknowledged claim stands at the insurer, as
// Payahub last heard, and lets a person ask again now.
export function WithInsurer({
  id,
  status,
  message,
  checkedAt,
  canAsk,
}: {
  id: string;
  status?: string;
  message?: string;
  checkedAt?: string;
  canAsk: boolean;
}) {
  const [s, action] = useActionState(askInsurer, {} as FormState);
  const queried = status === "queried";
  return (
    <section
      className={`app-card app-insurer${queried ? " app-insurer--query" : ""}`}
      aria-labelledby="ins-h"
    >
      <div className="app-insurer__body">
        <h2 id="ins-h" className="app-card__title">
          With the insurer
        </h2>
        <p>
          {status
            ? (statusText[status] ?? status)
            : "Payahub asks the insurer how it is going, soon after acknowledgement and then less often, until it is paid or rejected."}
        </p>
        {message && (
          <p className={queried ? "app-insurer__question" : "app-muted"}>
            &ldquo;{message}&rdquo;
          </p>
        )}
        <p className="app-muted">
          {checkedAt ? `Last asked ${dateTime(checkedAt)}.` : "Not asked yet."}
        </p>
      </div>
      {canAsk && (
        <form action={action} className="app-insurer__ask">
          <input type="hidden" name="id" value={id} />
          <Submit className="app-button">Ask the insurer now</Submit>
          <Messages state={s} />
        </form>
      )}
    </section>
  );
}
