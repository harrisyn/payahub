"use client";

import { useEffect, useRef, useState } from "react";

// Small looping pictures for the landing page. Each one is a real piece of
// the product's behaviour, drawn as interface, and each loops only while it
// is on screen. With reduced motion they hold their final, complete state.

function useLoop(steps: number, every: number) {
  const ref = useRef<HTMLDivElement>(null);
  const [i, setI] = useState(steps - 1);
  useEffect(() => {
    const el = ref.current;
    if (!el || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    let t: ReturnType<typeof setInterval> | undefined;
    const io = new IntersectionObserver(([e]) => {
      clearInterval(t);
      if (e.isIntersecting) {
        setI(0);
        t = setInterval(() => setI((n) => (n + 1) % steps), every);
      }
    });
    io.observe(el);
    return () => {
      clearInterval(t);
      io.disconnect();
    };
  }, [steps, every]);
  return [ref, i] as const;
}

const methods = [
  { name: "REST API", soon: false },
  { name: "Spreadsheet", soon: true },
  { name: "Scanned form", soon: true },
  { name: "Web form", soon: true },
];

const incoming = [
  { id: "clm_01JA7Q", line: "GP consultation", amount: "GHS 150.00", payer: "XCeli" },
  { id: "clm_01JA7R", line: "Malaria RDT", amount: "GHS 40.00", payer: "Medserve" },
  { id: "clm_01JA7S", line: "Full blood count", amount: "GHS 85.00", payer: "SmartClaims" },
];

/** Step 1: however the clinic bills, the claim arrives in one shape. */
export function IntakeVisual() {
  const [ref, i] = useLoop(incoming.length, 1800);
  const c = incoming[i];
  return (
    <div ref={ref} className="viz viz--intake" aria-hidden="true" data-ui>
      <div className="viz__chips">
        {methods.map((m, n) => (
          <span key={m.name} className={`viz__chip ${n === 0 ? "is-on" : ""}`}>
            {m.name}
            {m.soon && <em>soon</em>}
          </span>
        ))}
      </div>
      <div className="viz__card" key={i}>
        <div className="viz__row">
          <code className="viz__code">POST /v1/claims</code>
          <span className="viz__status viz__status--ok">202 Accepted</span>
        </div>
        <div className="viz__row">
          <span>{c.line}</span>
          <span className="viz__num">{c.amount}</span>
        </div>
        <div className="viz__row">
          <span className="viz__muted">To {c.payer}</span>
          <code className="viz__code viz__muted">{c.id}…</code>
        </div>
      </div>
    </div>
  );
}

const coding = [
  { label: "Your line", value: "Para 500 tabs x20", hint: "Your code PARA-500" },
  { label: "Standard item", value: "Paracetamol 500 mg tablet", hint: "Matched, 97% confident" },
  { label: "Insurer code", value: "Each insurer's own code", hint: "From the payer's list" },
];

/** Step 2: the line is matched to the standard list, then to each insurer's code. */
export function CodingVisual() {
  const [ref, i] = useLoop(coding.length + 1, 1300);
  return (
    <div ref={ref} className="viz viz--coding" aria-hidden="true" data-ui>
      {coding.map((c, n) => (
        <div key={c.label} className={`viz__map ${n <= i ? "is-on" : ""}`}>
          <span className="viz__muted">{c.label}</span>
          <span className="viz__map-value">{c.value}</span>
          <span className="viz__map-hint">{c.hint}</span>
        </div>
      ))}
      <div className={`viz__checks ${i >= coding.length ? "is-on" : ""}`}>
        <span>Member eligible</span>
        <span>ICD-10 B54 valid</span>
        <span>Price on list</span>
      </div>
    </div>
  );
}

const trail = [
  { what: "Queued", detail: "Saved with its job, one transaction", tone: "" },
  { what: "Sent", detail: "Attempt 1", tone: "" },
  { what: "Timed out", detail: "No answer within 60 seconds", tone: "warn" },
  { what: "Asked first", detail: "Insurer has no copy, safe to resend", tone: "" },
  { what: "Acknowledged", detail: "Insurer ref XC-88213", tone: "good" },
];

/** Step 3: delivery with retries that never double-send, all on record. */
export function DeliveryVisual() {
  const [ref, i] = useLoop(trail.length + 2, 1000);
  return (
    <div ref={ref} className="viz viz--trail" aria-hidden="true" data-ui>
      <ol className="viz__trail">
        {trail.map((t, n) => (
          <li key={t.what} className={`viz__step viz__step--${t.tone || "plain"} ${n <= i ? "is-on" : ""}`}>
            <span className="viz__step-dot" />
            <span className="viz__step-what">{t.what}</span>
            <span className="viz__step-detail">{t.detail}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

const fields = [
  { name: "Member number", value: "EQ25834301945-0", conf: 99 },
  { name: "Encounter date", value: "20 Oct 2026", conf: 98 },
  { name: "Diagnosis", value: "B54 Unspecified malaria", conf: 96 },
  { name: "Line 2", value: "Amoxicillin 500 mg capsule", conf: 81 },
];

/** AI reads a scanned form; fields under the bar wait for a person. */
export function ReviewVisual() {
  const [ref, i] = useLoop(fields.length + 2, 900);
  return (
    <div ref={ref} className="ai-card" aria-hidden="true" data-ui>
      <div className="ai-card__head">
        <span className="ai-card__file">claim-form-0193.jpg</span>
        <span className="ai-card__bar-note">Bar: 95%</span>
      </div>
      <ul className="ai-card__fields">
        {fields.map((f, n) => {
          const low = f.conf < 95;
          return (
            <li key={f.name} className={`ai-field ${n <= i ? "is-on" : ""} ${low ? "is-low" : ""}`}>
              <span className="ai-field__name">{f.name}</span>
              <span className="ai-field__value">{f.value}</span>
              <span className="ai-field__meter">
                <span style={{ width: n <= i ? `${f.conf}%` : "0%" }} />
              </span>
              <span className="ai-field__conf">{f.conf}%</span>
            </li>
          );
        })}
      </ul>
      <div className={`ai-card__foot ${i > fields.length ? "is-on" : ""}`}>
        <span className="ai-card__flag">1 field below the bar</span>
        <span>Waits for a person before anything is sent</span>
      </div>
    </div>
  );
}

const moves = [
  { via: "Medserve", note: "Through a shared platform" },
  { via: "Its own platform", note: "Moved this month" },
];

/** An insurer changes platform; the clinic's request does not change. */
export function SwitchVisual() {
  const [ref, i] = useLoop(moves.length, 2600);
  const m = moves[i];
  return (
    <div ref={ref} className="viz viz--switch" aria-hidden="true" data-ui>
      <div className="viz__card viz__card--still">
        <span className="viz__muted">Your request</span>
        <code className="viz__code">POST /v1/claims</code>
        <span>Same body, same insurer id</span>
        <span className="viz__same">Unchanged before and after</span>
      </div>
      <div className="viz__route">
        <span className="viz__muted">The insurer receives it through</span>
        <span className="viz__route-via" key={i}>
          {m.via}
        </span>
        <span className="viz__map-hint">{m.note}</span>
      </div>
      <p className="viz__whose">Updating the route is our job, not the clinic&apos;s.</p>
    </div>
  );
}
