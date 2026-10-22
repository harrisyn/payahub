import type { Metadata } from "next";
import Link from "next/link";

import { site } from "@/app/site";

export const metadata: Metadata = {
  title: "API reference",
  description: `How to send insurance claims with the ${site.name} API: authentication, idempotency, claims, insurers, claim states, errors and the sandbox.`,
  alternates: { canonical: "/docs" },
};

const endpoints = [
  { m: "POST", p: "/v1/claims", d: "Create a claim. Send an Idempotency-Key; a repeat returns the first claim with Idempotent-Replayed: true." },
  { m: "POST", p: "/v1/claims/validate", d: "Check a claim against the insurer's rules without saving or sending it." },
  { m: "GET", p: "/v1/claims", d: "List claims, newest first. Filter with state; page with before and limit." },
  { m: "GET", p: "/v1/claims/{id}", d: "Fetch one claim." },
  { m: "GET", p: "/v1/claims/{id}/events", d: "The claim's audit trail: every state change and attempt." },
  { m: "POST", p: "/v1/claims/{id}/submit", d: "Check and queue the claim for its insurer. Answers 202 at once; the outcome lands on the claim." },
  { m: "GET", p: "/v1/payers", d: "The shared list of insurers: standalone or behind a platform, and whether each is live." },
  { m: "GET", p: "/v1/connectors", d: "The platforms and insurer interfaces, with the account fields each needs." },
  { m: "GET", p: "/v1/items", d: "The standard list of services, lab tests, imaging, procedures, medicines and consumables. Filter with kind or q." },
  { m: "GET", p: "/v1/items/{id}", d: "One standard item and each insurer's code for it." },
  { m: "POST", p: "/v1/items/suggest", d: "Suggest standard items for your own items (code and name), with a confidence for each." },
  { m: "GET", p: "/v1/item-mappings", d: "Your codes mapped to standard items." },
  { m: "PUT", p: "/v1/item-mappings", d: "Save up to 500 mappings at once, all or none. Safe to repeat." },
  { m: "DELETE", p: "/v1/item-mappings/{code}", d: "Remove one mapping." },
  { m: "GET", p: "/v1/health", d: "Service health." },
];

const states = [
  ["draft", "Received; not yet checked, or a check found something to fix."],
  ["needs_review", "An AI-set field is below its confidence bar; a person must confirm it."],
  ["ready", "Checked and queued to send."],
  ["submitted", "With the insurer, waiting for an answer, or being retried."],
  ["acknowledged", "The insurer has the claim and gave its number. This is what is billed."],
  ["rejected", "The insurer refused it. Fix and submit again."],
  ["approved, paid", "Later answers from the insurer."],
  ["withdrawn", "Taken back before payment."],
];

const errors = [
  ["401 unauthenticated / invalid_api_key", "No key, or a key we do not recognise."],
  ["422 invalid_claim", "Missing or invalid fields; details lists each one."],
  ["422 unknown_payer", "No supported insurer with that payer_id."],
  ["422 idempotency_conflict", "This Idempotency-Key was used with a different body."],
  ["422 not_submittable", "The claim failed its checks; details lists the issues."],
  ["402 trial_limit", "The trial has no live claims left; choose a plan."],
  ["404 claim_not_found", "No claim with that id in your organisation."],
];

const sandbox = [
  ["NOTFOUND…", "Member not found; the claim is rejected."],
  ["OTP…", "The member needs a code; the sandbox code is 123456."],
  ["REJECT…", "The insurer refuses the claim."],
  ["FLAKY…", "The first send times out and is retried."],
  ["LOSTREPLY…", "The insurer accepts but the reply is lost; the retry finds the claim instead of sending it again."],
  ["anything else", "Eligible and acknowledged."],
];

export default function Docs() {
  return (
    <div className="docs">
      <header className="nav wrap">
        <Link href="/" className="wordmark">
          {site.name}
        </Link>
        <nav aria-label="Main" className="nav__links">
          <Link href="/#pricing">Pricing</Link>
          <Link href="/signin">Sign in</Link>
        </nav>
        <Link href="/start" className="button nav__action">
          Get a sandbox key
        </Link>
      </header>
      <main className="wrap docs__grid">
        <nav className="docs__toc" aria-label="On this page">
          <a href="#quick-start">Quick start</a>
          <a href="#auth">Authentication</a>
          <a href="#idempotency">Idempotency</a>
          <a href="#payers">Insurers and platforms</a>
          <a href="#items">Standard items</a>
          <a href="#endpoints">Endpoints</a>
          <a href="#states">Claim states</a>
          <a href="#errors">Errors</a>
          <a href="#sandbox">Sandbox</a>
        </nav>
        <article className="docs__body">
          <h1 className="h-head">API reference</h1>
          <p className="docs__lead">
            One REST API for every insurer. Send a claim in one shape; we check it, build what each insurer needs and
            send it. The full spec is <a href="https://github.com/harrisyn/payahub/blob/main/openapi/payahub.v1.yaml">OpenAPI 3.1</a>.
          </p>

          <h2 id="quick-start">Quick start</h2>
          <p>
            <Link href="/start">Sign up</Link> and copy your sandbox key. Then create a claim and submit it. Money is in
            minor units: 15000 is GHS 150.00.
          </p>
          <pre>
            <code>{`curl https://api.payahub.example/v1/claims \\
  -H "Authorization: Bearer sk_sandbox_…" \\
  -H "Idempotency-Key: visit-20931" \\
  -d '{
    "payer_id": "sandbox",
    "member": { "member_number": "M-1001" },
    "provider": { "name": "Ridge Clinic" },
    "encounter": { "start_date": "2026-10-20" },
    "lines": [{ "description": "Consultation", "unit_price": 15000,
                "code": { "system": "payahub", "value": "SRV-0001" } }],
    "diagnoses": [{ "code": { "system": "icd-10", "value": "B54" }, "role": "primary" }]
  }'

curl -X POST https://api.payahub.example/v1/claims/clm_…/submit \\
  -H "Authorization: Bearer sk_sandbox_…"`}</code>
          </pre>
          <p>
            Submit answers <code>202</code> straight away. The claim moves to <code>ready</code>, then{" "}
            <code>submitted</code>, then the insurer&apos;s answer. Read it from <code>GET /v1/claims/{"{id}"}</code> or
            its events.
          </p>

          <h2 id="auth">Authentication</h2>
          <p>
            Send your key as <code>Authorization: Bearer &lt;key&gt;</code>. Sandbox keys (<code>sk_sandbox_…</code>)
            reach only the sandbox insurer; live keys (<code>sk_live_…</code>) send real claims. Make and revoke keys in
            the console. A key cannot manage keys, insurers or credentials: those need a signed-in person.
          </p>

          <h2 id="idempotency">Idempotency</h2>
          <p>
            Every write takes an <code>Idempotency-Key</code>. Use something your system already has, such as the visit
            or invoice id. A repeat with the same body returns the first result; the same key with a different body is
            refused. Retrying a request after a timeout is always safe.
          </p>

          <h2 id="payers">Insurers and platforms</h2>
          <p>
            Insurers are one shared list, set up once by us for every clinic. Some are reached directly; most sit behind
            a platform such as XCeli, Medserve or RX, which carries claims for several insurers. Send{" "}
            <code>payer_id</code> from <code>GET /v1/payers</code>. For an insurer behind a platform you add your account
            on that platform once, in the console, and it serves every insurer the platform carries.
          </p>

          <h2 id="items">Standard items and your codes</h2>
          <p>
            Every line resolves to a standard item, and the standard item to the insurer&apos;s own code. Send a line
            either with a standard code, <code>{`{"system": "payahub", "value": "SRV-0001"}`}</code>, or with your own
            code once you have mapped it (<code>PUT /v1/item-mappings</code> or the console). A line that already
            carries the insurer&apos;s code in <code>mapping.target</code> is sent as it is.
          </p>
          <pre>
            <code>{`"lines": [
  { "description": "OPD consultation", "unit_price": 15000,
    "code": { "system": "local", "value": "CONS" } },
  { "description": "Artemether-lumefantrine", "unit_price": 3000,
    "code": { "system": "payahub", "value": "MED-0003" } }
]`}</code>
          </pre>
          <p>
            The claim comes back with <code>item</code> and <code>mapping</code> filled in on each line it could resolve.
            A line it could not resolve stops the claim at submit with an issue saying which code to map.
          </p>

          <h2 id="endpoints">Endpoints</h2>
          <table className="docs__table">
            <tbody>
              {endpoints.map((e) => (
                <tr key={e.m + e.p}>
                  <td>
                    <code>
                      {e.m} {e.p}
                    </code>
                  </td>
                  <td>{e.d}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <h2 id="states">Claim states</h2>
          <table className="docs__table">
            <tbody>
              {states.map(([s, d]) => (
                <tr key={s}>
                  <td>
                    <code>{s}</code>
                  </td>
                  <td>{d}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>
            A claim that cannot reach its insurer is retried with growing pauses, up to 20 times. Before each retry we ask
            the insurer whether it already has the claim, so a lost reply never becomes a second claim.
          </p>

          <h2 id="errors">Errors</h2>
          <p>
            Every error has the same shape: <code>{`{"error": {"code", "message", "details"}}`}</code>. Match on{" "}
            <code>code</code>; the message is for people.
          </p>
          <table className="docs__table">
            <tbody>
              {errors.map(([c, d]) => (
                <tr key={c}>
                  <td>
                    <code>{c}</code>
                  </td>
                  <td>{d}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <h2 id="sandbox">Sandbox</h2>
          <p>The sandbox insurer follows the member number, so you can test every path before going live.</p>
          <table className="docs__table">
            <tbody>
              {sandbox.map(([m, d]) => (
                <tr key={m}>
                  <td>
                    <code>{m}</code>
                  </td>
                  <td>{d}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </article>
      </main>
    </div>
  );
}
