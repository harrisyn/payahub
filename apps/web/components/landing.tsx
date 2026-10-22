"use client";

import Link from "next/link";
import { useEffect, useId, useRef, useState } from "react";

import { ghs, monthlyCost, plans, TRIAL_DAYS, TRIAL_LIVE_CLAIMS } from "@/lib/pricing";

import { runSandbox, type SandboxRun } from "./sandbox";

const reducedMotion = () =>
  typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

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

const slotStates = [
  { label: "Queued", tone: "wait" },
  { label: "Sent", tone: "wait" },
  { label: "Acknowledged", tone: "good" },
] as const;

// HeroSlot is the word the headline shows instead of saying: one claim,
// moving through its states the way every claim does.
export function HeroSlot() {
  const [i, setI] = useState(2);
  useEffect(() => {
    if (reducedMotion()) return;
    setI(0);
    const t = setInterval(() => setI((n) => (n + 1) % slotStates.length), 1700);
    return () => clearInterval(t);
  }, []);
  const s = slotStates[i];
  return (
    <span className="slot shutter" data-ui aria-hidden="true">
      <span className="slot__row">
        <span>Ridge Clinic</span>
        <span className="num">GHS 150.00</span>
      </span>
      <span className="slot__row">
        <span className={`pill pill--${s.tone}`}>{s.label}</span>
        <span className="slot__ref num">{s.tone === "good" ? "SBX-3F9A0C2E71" : "clm_01J…"}</span>
      </span>
    </span>
  );
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
    <div className="try" data-ui>
      <label htmlFor={id} className="try__label">
        Member number
      </label>
      <div className="try__line">
        <input
          id={id}
          className="try__input"
          value={value}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setValue(e.target.value.slice(0, 24))}
        />
      </div>
      <div className="try__answer" aria-live="polite">
        {run && (
          <>
            <div className="try__row">
              <span className="try__key">Member check</span>
              <span className={run.member.ok ? "" : "is-bad"}>{run.member.text}</span>
            </div>
            <div className="try__row">
              <span className="try__key">Outcome</span>
              <span className={run.outcome.tone === "bad" ? "is-bad" : ""}>{run.outcome.text}</span>
            </div>
            <div className="try__row">
              <span className="try__key">Audit trail</span>
              <ol className="try__events">
                {run.events.map((e, n) => (
                  <li key={n}>
                    <span className="try__event">{e.kind.replaceAll("_", " ")}</span> {e.detail}
                  </li>
                ))}
              </ol>
            </div>
            <div className="try__row">
              <span className="try__key">Sent to insurer</span>
              <span className="num">
                {run.sends} {run.sends === 1 ? "time" : "times"}
              </span>
            </div>
          </>
        )}
      </div>
      <div className="try__examples">
        {examples.map((ex) => (
          <button key={ex} type="button" className="chip" onClick={() => setValue(ex)}>
            {ex}
          </button>
        ))}
      </div>
    </div>
  );
}

const names = ["XCeli", "Medserve", "SmartClaims", "RX Insurance", "ClaimIQ"];

export function ProofNames() {
  const [lit, setLit] = useState(0);
  useEffect(() => {
    if (reducedMotion()) return;
    const t = setInterval(() => setLit((n) => (n + 1) % names.length), 2000);
    return () => clearInterval(t);
  }, []);
  return (
    <p className="names">
      Ported from five integrations live in Ghanaian clinics:{" "}
      {names.map((n, i) => (
        <span key={n}>
          <span className={i === lit ? "names__lit" : "names__dim"}>{n}</span>
          {i < names.length - 1 ? ", " : "."}
        </span>
      ))}
    </p>
  );
}

const switches = [
  {
    text: "Send each claim the moment it is ready",
    then: "One API call per claim. It is saved and queued in the same transaction, so an accepted claim is never lost.",
    field: "white",
    on: true,
  },
  {
    text: "Hold any code we are unsure of",
    then: "A code the AI mapped below 95% confidence waits for a person. Nothing guessed reaches an insurer.",
    field: "navy",
    on: true,
  },
  {
    text: "Retry until the insurer answers",
    then: "Up to 20 attempts with growing pauses. Each retry asks the insurer first, so nothing is sent twice.",
    field: "surface",
    on: false,
  },
  {
    text: "Keep insurer passwords out of sight",
    then: "Sealed with AES-256 before they are stored. Nobody can read them back from the console.",
    field: "indigo",
    on: false,
  },
  {
    text: "Map each code once, for every insurer",
    then: "Match your price list to our standard list of services and medicines. Every insurer's own codes follow from it.",
    field: "navy",
    on: false,
  },
  {
    text: "Draft claims from scanned forms",
    then: "Upload PDFs or photos and check the drafts we make. In build now; the API is ready today.",
    field: "white",
    on: false,
  },
];

export function Switches() {
  const [on, setOn] = useState(switches.map((s) => s.on));
  return (
    <ul className="switches">
      {switches.map((s, i) => (
        <li key={s.text} className={`band band--${s.field} ${on[i] ? "is-on" : ""}`}>
          <div className="band__text">
            <p className="band__sentence">{s.text}</p>
            <p className="band__then" hidden={!on[i]}>
              {s.then}
            </p>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={on[i]}
            aria-label={s.text}
            className="toggle"
            data-ui
            onClick={() => setOn((v) => v.map((x, n) => (n === i ? !x : x)))}
          >
            <span className="toggle__knob" />
          </button>
        </li>
      ))}
    </ul>
  );
}

export function Pricing() {
  const [sel, setSel] = useState(1);
  const [claims, setClaims] = useState(800);
  const plan = plans[sel];
  const cost = monthlyCost(plan, claims);
  const sliderId = useId();
  const listRef = useRef<HTMLDivElement>(null);
  return (
    <div className="pricing">
      <div className="pricing__names" role="tablist" aria-label="Plans" ref={listRef}>
        {plans.map((p, i) => (
          <button
            key={p.id}
            type="button"
            role="tab"
            aria-selected={i === sel}
            className="pricing__name"
            onClick={() => setSel(i)}
            onFocus={() => setSel(i)}
          >
            {p.name}
          </button>
        ))}
      </div>
      <div className="pricing__stage" role="tabpanel" aria-live="polite">
        <div key={plan.id} className="pricing__price">
          <span className="pricing__figure num">{cost === null ? "Agreed" : ghs(cost)}</span>
          <span className="pricing__period">{cost === null ? "with you, in a contract" : "a month"}</span>
        </div>
        <div className="pricing__volume">
          <label htmlFor={sliderId}>
            At <strong className="num">{claims.toLocaleString("en-GH")}</strong> acknowledged claims a month
          </label>
          <input
            id={sliderId}
            type="range"
            min={50}
            max={5000}
            step={50}
            value={claims}
            onChange={(e) => setClaims(Number(e.target.value))}
          />
        </div>
        <p className="pricing__summary">{plan.summary}</p>
        <Link className="button" href={plan.id === "enterprise" ? "/start?plan=enterprise" : `/start?plan=${plan.id}`}>
          {plan.id === "enterprise" ? "Talk to us about Enterprise" : `Start the trial, then ${plan.name}`}
        </Link>
        <p className="pricing__trial">
          Every plan starts with {TRIAL_DAYS} days free: the whole sandbox and {TRIAL_LIVE_CLAIMS} live claims, no card.
          Only claims an insurer acknowledges are ever billed.
        </p>
      </div>
    </div>
  );
}
