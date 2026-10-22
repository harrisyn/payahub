import type { Metadata } from "next";
import Image from "next/image";
import Link from "next/link";

import { HeroSlot, Pricing, ProofNames, Reveal, SandboxTry, Switches } from "@/components/landing";
import { Logo } from "@/components/logo";
import { plans, TRIAL_DAYS, TRIAL_LIVE_CLAIMS } from "@/lib/pricing";

import { site } from "./site";

export const metadata: Metadata = {
  title: { absolute: `${site.name}: send every insurance claim to every insurer, from one API` },
  description: site.description,
  alternates: { canonical: "/" },
};

const stages = [
  { minutes: 2, label: "Two minutes", clause: "First, sign up with your work email and get a sandbox key at once." },
  { minutes: 3, label: "Three minutes", clause: "Then tell us how you bill today, and we recommend how to connect." },
  { minutes: 9, label: "About nine minutes", clause: "Next, pick your insurers and add the credentials they gave you." },
  { minutes: 1, label: "One request", clause: "Last, send a claim and watch the insurer acknowledge it." },
];

const faqs = [
  {
    q: "Which insurers can we send to?",
    a: "Pick yours from one shared list. Most insurers sit behind a platform (XCeli, Medserve, SmartClaims, RX Insurance or ClaimIQ, ported from integrations already working in Ghanaian clinics); you add your account on that platform once and it covers every insurer it carries. Your console shows each one's status before you rely on it.",
  },
  {
    q: "Do we retype our price list for each insurer?",
    a: "No. Map your own codes for services and medicines to our standard list once, with suggestions to confirm. Each insurer's own codes follow from the standard item, so one claim line reaches every insurer correctly coded.",
  },
  {
    q: "What counts as a claim on our bill?",
    a: "Only a claim an insurer has acknowledged. Drafts, checks, claims we stop before sending and resends of the same claim are never billed.",
  },
  {
    q: "What happens when an insurer's system is down?",
    a: "The claim waits in a durable queue and is retried with growing pauses. Before every retry we ask the insurer whether it already has the claim, so it is never lost and never sent twice.",
  },
  {
    q: "Does AI decide what goes to the insurer?",
    a: "No. AI proposes codes, with its confidence and the evidence for each. Rules build every request that leaves, and anything below the confidence bar waits for a person.",
  },
  {
    q: "We have no developers. Can we still use it?",
    a: "Yes. Onboarding asks how you bill today and recommends a route: the API for software, a spreadsheet upload, scanned forms, or a web form for a few claims a month.",
  },
  {
    q: "How are our insurer passwords kept?",
    a: "Each set is sealed with AES-256 before it is stored, bound to your organisation and that insurer. The console shows which fields are set, never their values.",
  },
];

// What the console's payer list shows today, from the connector manifests.
const connectors = [
  { name: "Sandbox", channel: "API", otp: "Test code", status: "Available" },
  { name: "XCeli", channel: "API", otp: "SMS code", status: "Connecting" },
  { name: "Medserve", channel: "API", otp: "SMS code", status: "Connecting" },
  { name: "SmartClaims", channel: "API", otp: "SMS code", status: "Connecting" },
  { name: "RX Insurance", channel: "API", otp: "SMS code", status: "Connecting" },
  { name: "ClaimIQ", channel: "API", otp: "None", status: "Connecting" },
];

const jsonLd = [
  {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: site.name,
    applicationCategory: "BusinessApplication",
    operatingSystem: "Web",
    description: site.description,
    url: site.url,
    image: `${site.url}/brand/symbol-color-1024.png`,
    offers: plans
      .filter((p) => p.monthlyFee !== null || p.perClaim !== null)
      .map((p) => ({
        "@type": "Offer",
        name: p.name,
        priceCurrency: "GHS",
        price: p.monthlyFee ?? p.perClaim,
        description: p.summary,
      })),
  },
  {
    "@context": "https://schema.org",
    "@type": "FAQPage",
    mainEntity: faqs.map((f) => ({ "@type": "Question", name: f.q, acceptedAnswer: { "@type": "Answer", text: f.a } })),
  },
];

const curl = `curl ${site.url.replace("https://", "https://api.")}/v1/claims \\
  -H "Authorization: Bearer sk_sandbox_…" \\
  -H "Idempotency-Key: visit-20931" \\
  -d '{
    "payer_id": "xceli",
    "member": { "member_number": "EQ25834301945-0" },
    "provider": { "name": "Ridge Clinic" },
    "encounter": { "start_date": "2026-10-20" },
    "lines": [{ "description": "Consultation, GP",
                "unit_price": 15000 }],
    "diagnoses": [{ "code": { "system": "icd-10",
                    "value": "B54" }, "role": "primary" }]
  }'`;

const reply = `202 Accepted
{ "claim": { "id": "clm_01J…", "state": "ready",
             "total": 15000, "currency": "GHS" },
  "issues": [] }`;

export default function Home() {
  return (
    <>
      <Reveal />
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }} />

      <header className="nav wrap" id="top">
        <Link href="/" className="nav__brand" aria-label={`${site.name} home`}>
          <Logo />
        </Link>
        <nav aria-label="Main" className="nav__links">
          <a href="#how">How it works</a>
          <a href="#pricing">Pricing</a>
          <Link href="/docs">API docs</Link>
          <Link href="/signin">Sign in</Link>
        </nav>
        <Link href="/start" className="button nav__action">
          Start free trial
        </Link>
      </header>

      <main>
        <section className="hero wrap">
          <h1 className="hero__title">
            <span className="hero__line">Every claim</span> <HeroSlot />{" "}
            <span className="hero__line">to every insurer</span>
          </h1>
          <div className="hero__foot">
            <p className="hero__lead">
              One API sends your clinic&apos;s claims to every insurer in Ghana and never loses one.
            </p>
            <div className="hero__actions">
              <Link href="/start" className="button button--large">
                Start your free trial
              </Link>
              <Link href="/docs" className="link">
                Read the API docs
              </Link>
            </div>
          </div>
        </section>

        <section className="intro field-white" aria-labelledby="intro-h">
          <div className="wrap">
            <h2 id="intro-h" className="h-head" data-reveal>
              Month-end, as it is today
            </h2>
            <p className="intro__statement" data-reveal>
              One claim leaves as{" "}
              <span className="thumb thumb--portal" data-ui aria-hidden="true">
                <span className="thumb__bar" />
                <span className="thumb__field">Username</span>
                <span className="thumb__field">Password</span>
              </span>{" "}
              five portal logins, a{" "}
              <span className="thumb thumb--sheet" data-ui aria-hidden="true">
                <span>B54</span>
                <span className="num">150.00</span>
                <span>J06</span>
                <span className="num">85.00</span>
              </span>{" "}
              spreadsheet and a{" "}
              <span className="thumb thumb--paper" data-ui aria-hidden="true">
                <span className="thumb__line" />
                <span className="thumb__line thumb__line--short" />
                <span className="thumb__stamp">Claim form</span>
              </span>{" "}
              paper form, then comes back weeks later, rejected over one code.
            </p>
          </div>
        </section>

        <section id="how" className="how wrap" aria-labelledby="how-h">
          <h2 id="how-h" className="h-head" data-reveal>
            Fifteen minutes to a first claim
          </h2>
          <ol className="stages" data-ui data-reveal>
            {stages.map((s, i) => (
              <li key={s.label} className={`stage stage--${i}`} style={{ flexGrow: s.minutes }}>
                <span className="stage__block">
                  <span className="stage__time">{s.label}</span>
                </span>
                <span className="stage__clause">{s.clause}</span>
              </li>
            ))}
          </ol>
        </section>

        <section className="showcase field-navy" aria-labelledby="try-h">
          <div className="wrap showcase__grid">
            <div className="showcase__copy">
              <h2 id="try-h" className="h-head" data-reveal>
                Send a test claim
              </h2>
              <p className="showcase__text" data-reveal>
                Type a member number. This runs the same rules as the sandbox your trial gets, failures included: a
                refusal, a timeout that is retried, and a reply lost on the way back.
              </p>
            </div>
            <SandboxTry />
          </div>
        </section>

        <section className="dev field-white" aria-labelledby="dev-h">
          <div className="wrap dev__grid">
            <div className="dev__copy">
              <h2 id="dev-h" className="h-head" data-reveal>
                One request per claim
              </h2>
              <p className="dev__text" data-reveal>
                Send the claim as your system holds it: one shape for every insurer, money in pesewas, an idempotency
                key so a retried request never makes a second claim. We work out what each insurer needs, check it,
                and send it.
              </p>
              <Link href="/docs" className="link dev__link" data-reveal>
                Read the API reference
              </Link>
            </div>
            <div className="dev__code" data-reveal>
              <pre>
                <code>{curl}</code>
              </pre>
              <pre className="dev__reply">
                <code>{reply}</code>
              </pre>
            </div>
          </div>
        </section>

        <section className="console field-navy" aria-labelledby="console-h">
          <div className="wrap console__grid">
            <div className="console__copy">
              <h2 id="console-h" className="h-head" data-reveal>
                A console for the desk
              </h2>
              <p className="console__lead" data-reveal>
                Billing staff never need the API. The console walks a clinic to its first live claim and shows every
                attempt on every claim, so nobody calls the insurer to ask.
              </p>
            </div>
            <figure className="console__shot console__shot--main" data-reveal>
              <Image
                src="/console/claims-list.png"
                alt="The claims list: five sandbox claims, four acknowledged and one rejected, each with its amount and the insurer's claim number."
                width={1888}
                height={1126}
                sizes="(max-width: 64em) 100vw, 58vw"
              />
              <figcaption>Every claim, its state and the insurer&apos;s number.</figcaption>
            </figure>
            <figure className="console__shot console__shot--steps" data-reveal>
              <Image
                src="/console/first-claim-steps.png"
                alt="The five onboarding steps, all ticked: account, how you bill, insurers, a first claim, plan."
                width={1304}
                height={1006}
                sizes="(max-width: 64em) 100vw, 34vw"
              />
              <figcaption>Five steps from sign-up to a live claim.</figcaption>
            </figure>
            <figure className="console__shot console__shot--trail" data-reveal>
              <Image
                src="/console/audit-trail.png"
                alt="A claim's history: received, queued, a timed-out attempt, then acknowledged one second later."
                width={1174}
                height={604}
                sizes="(max-width: 64em) 100vw, 34vw"
              />
              <figcaption>A timeout, retried and acknowledged a second later.</figcaption>
            </figure>
            <figure className="console__shot console__shot--creds" data-reveal>
              <Image
                src="/console/insurer-credentials.png"
                alt="XCeli's settings: credentials saved and sealed, a connection check, and what the insurer supports."
                width={1888}
                height={936}
                sizes="(max-width: 64em) 100vw, 58vw"
              />
              <figcaption>Insurer credentials, sealed as they are saved.</figcaption>
            </figure>
          </div>
        </section>

        <section className="proof wrap" aria-labelledby="proof-h">
          <div className="proof__copy">
            <h2 id="proof-h" className="h-head" data-reveal>
              Working connectors
            </h2>
            <div data-reveal>
              <ProofNames />
            </div>
          </div>
          <div className="payers" data-ui data-reveal>
            <p className="payers__title">Insurers</p>
            <table className="payers__table">
              <thead>
                <tr>
                  <th scope="col">Platform</th>
                  <th scope="col">Channel</th>
                  <th scope="col">Member code</th>
                  <th scope="col">Status</th>
                </tr>
              </thead>
              <tbody>
                {connectors.map((c) => (
                  <tr key={c.name}>
                    <td>{c.name}</td>
                    <td>{c.channel}</td>
                    <td>{c.otp}</td>
                    <td>
                      <span className={`status status--${c.status === "Available" ? "good" : "wait"}`}>{c.status}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className="features" aria-labelledby="features-h">
          <h2 id="features-h" className="h-head wrap" data-reveal>
            Switch on what you need
          </h2>
          <Switches />
        </section>

        <section id="pricing" className="pricing-section field-white" aria-labelledby="pricing-h">
          <div className="wrap">
            <h2 id="pricing-h" className="h-head" data-reveal>
              Four ways to pay
            </h2>
            <Pricing />
          </div>
        </section>

        <section className="faq wrap" aria-labelledby="faq-h">
          <div className="faq__row">
            <h2 id="faq-h" className="faq__q faq__heading">
              Questions
            </h2>
            <span />
          </div>
          {faqs.map((f) => (
            <div className="faq__row" key={f.q}>
              <h3 className="faq__q">{f.q}</h3>
              <p className="faq__a">{f.a}</p>
            </div>
          ))}
        </section>

        <section className="cta field-navy" aria-labelledby="cta-h">
          <div className="wrap cta__inner">
            <h2 id="cta-h" className="cta__title">
              Your first claim, today
            </h2>
            <div className="cta__foot">
              <p className="cta__text">
                Your sandbox key is ready the moment you sign up. {TRIAL_DAYS} days and {TRIAL_LIVE_CLAIMS} live claims
                free, no card, no sales call.
              </p>
              <Link href="/start" className="button button--large">
                Start your free trial
              </Link>
            </div>
          </div>
        </section>
      </main>

      <footer className="footer">
        <div className="wrap footer__top">
          <p className="footer__notes">
            <Logo className="logo logo--foot" /> One API for insurance claims, built in {site.city}.
          </p>
          <a href="#top" className="link">
            Back to top
          </a>
        </div>
        <div className="wrap">
          <a className="footer__contact" href={`mailto:${site.email}`}>
            {site.email}
          </a>
          <div className="footer__meta">
            <span className="footer__links">
              <Link href="/docs">API docs</Link>
              <Link href="/signin">Sign in</Link>
              <Link href="/start">Start free trial</Link>
            </span>
            <span>
              {site.city}, {new Date().getFullYear()}
            </span>
          </div>
        </div>
      </footer>
    </>
  );
}
