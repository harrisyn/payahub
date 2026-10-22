import Link from "next/link";

import { sendTestClaim } from "@/app/actions";
import { ClaimsTable } from "@/components/claim-row";
import { Submit } from "@/components/forms";
import { KeyBanner } from "@/components/key-banner";
import { api } from "@/lib/api";
import { daysLeft, methodName } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { ClaimSummary, Payer } from "@/lib/types";

const stepCopy = {
  account: { title: "Create your account", detail: "Done. Your sandbox key was shown once at sign-up.", href: "/console/developers", cta: "Keys" },
  profile: { title: "Tell us how you bill", detail: "Four questions, then we recommend how to connect.", href: "/console/setup", cta: "Answer" },
  payers: { title: "Choose your insurers", detail: "Pick them from our list and add your account on the platform each one uses.", href: "/console/insurers", cta: "Choose" },
  items: { title: "Map your services and medicines", detail: "Match your own codes to our standard list once; every insurer's codes follow.", href: "/console/items", cta: "Map" },
  first_claim: { title: "Send a first claim", detail: "Send a sandbox claim and watch the insurer acknowledge it.", href: "/console/claims", cta: "Send" },
  go_live: { title: "Choose a plan and go live", detail: "Pick how to pay, make a live key, and send real claims.", href: "/console/plan", cta: "Choose" },
} as const;

export default async function Overview() {
  const view = await requireOrg();
  const { organization: org, usage, onboarding, recommendation } = view;
  const [claims, payers] = await Promise.all([
    api<{ data: ClaimSummary[] }>("/v1/claims?limit=5"),
    api<{ data: Payer[] }>("/v1/payers"),
  ]);
  const rows = claims.ok ? claims.data.data : [];
  const payerNames = Object.fromEntries((payers.ok ? payers.data.data : []).map((m) => [m.id, m.name]));
  const done = onboarding.filter((s) => s.done).length;
  const next = onboarding.find((s) => !s.done);
  const trialLeft = Math.max(0, usage.trial_live_limit - usage.live_claims);

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>{org.name}</h1>
          <p className="app-finding">
            {next
              ? `${done} of ${onboarding.length} steps done. Next: ${stepCopy[next.id].title.toLowerCase()}.`
              : `All ${onboarding.length} steps done. Live claims now go through your plan.`}
          </p>
        </div>
        {next && (
          <Link href={stepCopy[next.id].href} className="app-button app-button--primary">
            {stepCopy[next.id].title}
          </Link>
        )}
      </div>

      <KeyBanner />

      <div className="app-split">
        <section className="app-card" aria-labelledby="steps-h">
          <h2 id="steps-h" className="app-card__title">
            Getting to your first live claim
          </h2>
          <ol className="app-steps">
            {onboarding.map((s) => {
              const c = stepCopy[s.id];
              return (
                <li key={s.id} className={`app-step ${s.done ? "is-done" : ""} ${next?.id === s.id ? "is-next" : ""}`}>
                  <span className="app-step__mark" aria-hidden="true">
                    {s.done ? (
                      <svg viewBox="0 0 16 16">
                        <path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" strokeWidth="2" />
                      </svg>
                    ) : null}
                  </span>
                  <span className="app-step__text">
                    <span className="app-step__title">
                      {c.title}
                      <span className="sr-only">{s.done ? " (done)" : " (to do)"}</span>
                    </span>
                    <span className="app-muted">
                      {s.id === "profile" && recommendation
                        ? `Recommended: ${methodName[recommendation.method]}.`
                        : c.detail}
                    </span>
                  </span>
                  {!s.done && (
                    <Link href={c.href} className="app-button">
                      {c.cta}
                    </Link>
                  )}
                </li>
              );
            })}
          </ol>
        </section>

        <section className="app-card" aria-labelledby="trial-h">
          <h2 id="trial-h" className="app-card__title">
            {org.plan === "trial" ? "Your trial" : "This month"}
          </h2>
          <dl className="app-figures">
            {org.plan === "trial" && (
              <>
                <div>
                  <dt>Days left</dt>
                  <dd className="num">{daysLeft(org.trial_ends_at)}</dd>
                </div>
                <div>
                  <dt>Live claims left</dt>
                  <dd className="num">
                    {trialLeft}
                    <span className="app-unit"> of {usage.trial_live_limit}</span>
                  </dd>
                </div>
              </>
            )}
            <div>
              <dt>Sandbox claims this month</dt>
              <dd className="num">{usage.sandbox_claims}</dd>
            </div>
            <div>
              <dt>Billable live claims this month</dt>
              <dd className="num">{usage.billable_live}</dd>
            </div>
          </dl>
          <p className="app-muted">Only claims an insurer acknowledges count towards a bill.</p>
        </section>
      </div>

      <section className="app-card" aria-labelledby="recent-h">
        <div className="app-card__head">
          <h2 id="recent-h" className="app-card__title">
            Latest claims
          </h2>
          <div className="app-card__actions">
            <form action={sendTestClaim}>
              <input type="hidden" name="n" value={usage.sandbox_claims} />
              <Submit className="app-button">Send a test claim</Submit>
            </form>
            <Link href="/console/claims" className="app-link">
              View all
            </Link>
          </div>
        </div>
        {rows.length ? (
          <ClaimsTable rows={rows} payerNames={payerNames} />
        ) : (
          <p className="app-empty">
            No claims yet. Send a test claim to the sandbox insurer, or send one from your system with your sandbox key.
          </p>
        )}
      </section>
    </div>
  );
}
