"use client";

import Link from "next/link";
import { useActionState, useEffect, useState } from "react";

import { sendLead, type LeadKind, type LeadState } from "@/app/lead-actions";
import { NumberInput, Textarea, TextInput, UIProvider } from "@/components/ui";

// The landing page's call to action. One question first (what do you need?),
// then only the fields that answer needs. Links like #contact-payer open the
// form on that choice.

const choices: { kind: LeadKind; title: string; text: string }[] = [
  { kind: "contact", title: "I'm interested. Contact me.", text: "For clinics and hospitals. We call you back." },
  { kind: "platform", title: "My platform isn't listed", text: "For insurers and claims platforms." },
  { kind: "payer", title: "Do you have this payer?", text: "Ask us to add an insurer you bill." },
  { kind: "specs", title: "Share your interface specs", text: "For developers. Get the generic interface." },
];

const thanks: Record<LeadKind, string> = {
  contact: "Thank you. We will call or email you within one working day.",
  platform: "Thank you. We will be in touch to plan the connection with your team.",
  payer: "Thank you. We will tell you when we can reach this payer, and how soon.",
  specs: "Thank you. We will email you when the interface or the integration guide changes.",
};

function newKey() {
  return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : String(Date.now());
}

function usePayerCheck(name: string) {
  const [match, setMatch] = useState<{ label: string; description?: string } | null | undefined>(undefined);
  useEffect(() => {
    const q = name.trim();
    if (q.length < 3) {
      setMatch(undefined);
      return;
    }
    const ctl = new AbortController();
    const t = setTimeout(async () => {
      try {
        const res = await fetch(`/lookup/payers?q=${encodeURIComponent(q)}`, { signal: ctl.signal });
        const body = (await res.json()) as { data?: { label: string; description?: string }[] };
        setMatch(body.data?.[0] ?? null);
      } catch {
        // an aborted or failed check just shows nothing
      }
    }, 300);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [name]);
  return match;
}

export function LeadForm() {
  const [kind, setKind] = useState<LeadKind>("contact");
  const [key, setKey] = useState("");
  const [payer, setPayer] = useState("");
  const [state, action, pending] = useActionState(sendLead, {} as LeadState);
  const [done, setDone] = useState<LeadKind | null>(null);
  const match = usePayerCheck(kind === "payer" ? payer : "");

  useEffect(() => setKey(newKey()), []);
  useEffect(() => {
    const pick = () => {
      const k = window.location.hash.replace("#contact-", "") as LeadKind;
      if (choices.some((c) => c.kind === k)) {
        setKind(k);
        setDone(null);
        document.getElementById("contact")?.scrollIntoView({ block: "start" });
      }
    };
    pick();
    window.addEventListener("hashchange", pick);
    return () => window.removeEventListener("hashchange", pick);
  }, []);
  useEffect(() => {
    if (state.ok && state.kind) {
      setDone(state.kind);
      setKey(newKey());
    }
  }, [state]);

  const err = (f: string) => (state.kind === kind ? state.fields?.[f] : undefined);
  const val = (f: string) => (state.kind === kind && !state.ok ? state.values?.[f] : undefined);

  return (
    <UIProvider>
      <div className="lead" data-ui>
        <fieldset className="lead__choices">
          <legend className="sr-only">What would you like from us?</legend>
          {choices.map((c) => (
            <label key={c.kind} className={`lead__choice ${kind === c.kind ? "is-on" : ""}`}>
              <input
                type="radio"
                name="lead-kind"
                value={c.kind}
                checked={kind === c.kind}
                onChange={() => {
                  setKind(c.kind);
                  setDone(null);
                }}
              />
              <strong>{c.title}</strong>
              <span>{c.text}</span>
            </label>
          ))}
        </fieldset>

        {done === kind ? (
          <div className="lead__done" role="status">
            <p>{thanks[kind]}</p>
            {kind === "specs" && <SpecLinks />}
            <button type="button" className="lead__again" onClick={() => setDone(null)}>
              Send another
            </button>
          </div>
        ) : (
          <form action={action} className="lead__form" key={kind}>
            <input type="hidden" name="kind" value={kind} />
            <input type="hidden" name="idempotency_key" value={key} />
            <div className="lead__trap" aria-hidden="true">
              <label>
                Website <input type="text" name="website" tabIndex={-1} autoComplete="off" />
              </label>
            </div>

            {kind === "specs" && (
              <>
                <p className="lead__intro">
                  The interface is public. Read it now, and leave your email if you want to hear when it or the
                  integration guide for platforms changes.
                </p>
                <SpecLinks />
              </>
            )}

            {kind === "payer" && (
              <>
                <TextInput
                  name="payer_name"
                  label="Payer's name"
                  placeholder="The insurer you bill"
                  required
                  value={payer}
                  onChange={(e) => setPayer(e.currentTarget.value)}
                  error={err("payer_name")}
                  description={
                    match ? (
                      <span className="lead__match">
                        Already on our list: {match.label}
                        {match.description ? ` (${match.description.toLowerCase()})` : ""}. You can send claims to it
                        today.
                      </span>
                    ) : match === null ? (
                      "Not on our list yet. Tell us about it below."
                    ) : undefined
                  }
                />
                <div className="lead__row">
                  <NumberInput
                    name="monthly_claims"
                defaultValue={val("monthly_claims")}
                    label="Claims a month to this payer"
                    placeholder="About how many"
                    min={0}
                    allowDecimal={false}
                    allowNegative={false}
                    hideControls
                    error={err("monthly_claims")}
                  />
                  <TextInput
                    name="payer_contact"
                defaultValue={val("payer_contact")}
                    label="Who to talk to there"
                    placeholder="Name, email or phone"
                    error={err("payer_contact")}
                  />
                </div>
              </>
            )}

            {kind === "platform" && (
              <TextInput
                name="organization"
                defaultValue={val("organization")}
                label="Insurer or platform"
                placeholder="Its name"
                required
                error={err("organization")}
              />
            )}

            <div className="lead__row">
              <TextInput
                name="name"
                defaultValue={val("name")}
                label="Your name"
                autoComplete="name"
                required={kind === "contact"}
                error={err("name")}
              />
              {kind !== "platform" && (
                <TextInput
                  name="organization"
                defaultValue={val("organization")}
                  label={kind === "specs" ? "Company" : "Clinic or hospital"}
                  autoComplete="organization"
                  error={err("organization")}
                />
              )}
            </div>
            <div className="lead__row">
              <TextInput
                name="email"
                defaultValue={val("email")}
                type="email"
                label="Email"
                autoComplete="email"
                required
                error={err("email")}
              />
              <TextInput
                name="phone"
                defaultValue={val("phone")}
                type="tel"
                label={kind === "specs" ? "Phone (optional)" : "Phone"}
                autoComplete="tel"
                placeholder="024 123 4567"
                required={kind !== "specs"}
                error={err("phone")}
              />
            </div>
            {(kind === "contact" || kind === "platform") && (
              <Textarea
                name="message"
                defaultValue={val("message")}
                label={kind === "platform" ? "Which insurers do you carry? (optional)" : "Anything we should know? (optional)"}
                minRows={2}
                error={err("message")}
              />
            )}

            {state.error && state.kind === kind && (
              <p className="lead__error" role="alert">
                {state.error}
              </p>
            )}
            <div className="lead__actions">
              <button type="submit" className="lp-btn lp-btn--dark" disabled={pending || !key}>
                {pending ? "Sending…" : kind === "specs" ? "Keep me posted" : "Send"}
              </button>
              <span className="lead__fine">We use these details only to answer you.</span>
            </div>
          </form>
        )}
      </div>
    </UIProvider>
  );
}

function SpecLinks() {
  return (
    <ul className="lead__links">
      <li>
        <Link href="/docs">Read the API reference</Link>
      </li>
      <li>
        <a href="/openapi.yaml" download="payahub.v1.yaml">
          Download the OpenAPI spec
        </a>
      </li>
      <li>
        <a href="#developers">See a claim request and its reply</a>
      </li>
    </ul>
  );
}
