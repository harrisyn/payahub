"use client";

import Link from "next/link";
import { useEffect, useId, useState } from "react";

import { ghs, monthlyCost, plans, TRIAL_DAYS, TRIAL_LIVE_CLAIMS } from "@/lib/pricing";

import { runSandbox, type SandboxRun } from "./sandbox";

// Reveal marks the document as scripted and lets each [data-reveal] element
// rise in once as it enters. Without script nothing is hidden.
export function Reveal() {
  useEffect(() => {
    document.documentElement.classList.add("js");
    const els = document.querySelectorAll<HTMLElement>("[data-reveal]");
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            e.target.classList.add("is-in");
            io.unobserve(e.target);
          }
        }
      },
      { rootMargin: "0px 0px -8% 0px" },
    );
    els.forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, []);
  return null;
}

const examples = ["REJECT-22", "FLAKY-07", "LOSTREPLY-3"];

export function SandboxTry() {
  const [value, setValue] = useState("M-1001");
  const [run, setRun] = useState<SandboxRun | null>(null);
  const id = useId();
  useEffect(() => {
    let live = true;
    runSandbox(value).then((r) => live && setRun(r));
    return () => {
      live = false;
    };
  }, [value]);
  return (
    <div className="lp-try-card">
      <div className="lp-try-card__input">
        <label htmlFor={id}>Member number</label>
        <input
          id={id}
          value={value}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setValue(e.target.value.slice(0, 24))}
        />
      </div>
      <div className="lp-try-card__examples">
        <span>Try</span>
        {examples.map((ex) => (
          <button key={ex} type="button" className={value === ex ? "is-on" : ""} onClick={() => setValue(ex)}>
            {ex}
          </button>
        ))}
      </div>
      <div className="lp-try-card__answer" aria-live="polite">
        {run && (
          <>
            <p className={`lp-try-card__outcome lp-try-card__outcome--${run.outcome.tone}`}>{run.outcome.text}</p>
            <dl className="lp-try-card__facts">
              <div>
                <dt>Member check</dt>
                <dd className={run.member.ok ? "" : "is-bad"}>{run.member.text}</dd>
              </div>
              <div>
                <dt>Sent to insurer</dt>
                <dd>
                  {run.sends} {run.sends === 1 ? "time" : "times"}
                </dd>
              </div>
            </dl>
            <ol className="lp-try-card__events">
              {run.events.map((e, n) => (
                <li key={n}>
                  <span>{e.kind.replaceAll("_", " ")}</span> {e.detail}
                </li>
              ))}
            </ol>
          </>
        )}
      </div>
    </div>
  );
}

export function Pricing() {
  const [sel, setSel] = useState(1);
  const [claims, setClaims] = useState(800);
  const plan = plans[sel];
  const cost = monthlyCost(plan, claims);
  const sliderId = useId();
  return (
    <div className="lp-plans">
      <div className="lp-plans__tabs" role="tablist" aria-label="Plans">
        {plans.map((p, i) => (
          <button
            key={p.id}
            type="button"
            role="tab"
            id={`plan-${p.id}`}
            aria-selected={i === sel}
            aria-controls="plan-panel"
            className="lp-plans__tab"
            onClick={() => setSel(i)}
          >
            {p.name}
          </button>
        ))}
      </div>
      <div className="lp-plans__panel" id="plan-panel" role="tabpanel" aria-labelledby={`plan-${plan.id}`}>
        <div className="lp-plans__figure" aria-live="polite">
          <span className="lp-plans__amount">{cost === null ? "Agreed" : ghs(cost)}</span>
          <span className="lp-plans__period">{cost === null ? "with your group, in a contract" : "a month"}</span>
        </div>
        <p className="lp-plans__summary">{plan.summary}</p>
        <div className="lp-plans__volume">
          <label htmlFor={sliderId}>
            At <strong>{claims.toLocaleString("en-GH")}</strong> acknowledged claims a month
          </label>
          <input
            id={sliderId}
            type="range"
            min={50}
            max={5000}
            step={50}
            value={claims}
            disabled={cost === null}
            onChange={(e) => setClaims(Number(e.target.value))}
            style={{ ["--fill" as string]: `${((claims - 50) / 4950) * 100}%` }}
          />
        </div>
        <div className="lp-plans__foot">
          <Link className="lp-btn lp-btn--dark" href={`/start?plan=${plan.id}`}>
            {plan.id === "enterprise" ? "Talk to us about Enterprise" : `Start the trial, then ${plan.name}`}
          </Link>
          <p>
            Every plan starts with {TRIAL_DAYS} days free: the whole sandbox and {TRIAL_LIVE_CLAIMS} live claims, no
            card.
          </p>
        </div>
      </div>
    </div>
  );
}
