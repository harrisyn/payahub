import type { Metadata } from "next";
import Image from "next/image";
import Link from "next/link";

import { ClaimFlow } from "@/components/claim-flow";
import { Pricing, Reveal, SandboxTry } from "@/components/landing";
import { CodingVisual, DeliveryVisual, IntakeVisual, ReviewVisual, SwitchVisual } from "@/components/landing-visuals";
import { LeadForm } from "@/components/lead-form";
import { Logo } from "@/components/logo";
import { plans, TRIAL_DAYS, TRIAL_LIVE_CLAIMS } from "@/lib/pricing";

import "./landing.css";
import { site } from "./site";

export const metadata: Metadata = {
  title: { absolute: `${site.name}: health insurance claims to every insurer in Ghana, from one API` },
  description: site.description,
  alternates: { canonical: "/" },
  openGraph: {
    title: `${site.name}: every claim, every insurer, one hub`,
    description: site.description,
    url: "/",
    type: "website",
  },
};

const faqs = [
  {
    q: "Why do clinics need a claims hub now?",
    a: "More insurers in Ghana are moving off shared aggregators onto platforms of their own, so a clinic can end up with five to seven logins, forms and code lists. Payahub gives the clinic one interface and handles every platform behind it.",
  },
  {
    q: "What if our insurer moves to a different platform?",
    a: "Nothing changes on your side. Your claims keep the same shape and the same insurer, and we route them to the insurer's new platform.",
  },
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
    q: "We run an insurer or a claims platform. How do we connect?",
    a: "We are publishing an open integration guide covering member checks, claim submission, lookup by the clinic's reference, outcomes and code lists. Write to us for the draft, or to have your platform connected.",
  },
  {
    q: "How are our insurer passwords kept?",
    a: "Each set is sealed with AES-256 before it is stored, bound to your organisation and that insurer. The console shows which fields are set, never their values.",
  },
];



const jsonLd = [
  {
    "@context": "https://schema.org",
    "@type": "Organization",
    name: site.name,
    url: site.url,
    logo: `${site.url}/brand/symbol-color-1024.png`,
    email: site.email,
    address: { "@type": "PostalAddress", addressLocality: site.city, addressCountry: "GH" },
    areaServed: { "@type": "Country", name: "Ghana" },
  },
  {
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: site.name,
    url: site.url,
    inLanguage: "en-GH",
  },
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

const platforms = ["XCeli", "Medserve", "SmartClaims", "RX Insurance", "ClaimIQ"];

const steps = [
  {
    title: "Send claims the way you bill",
    text: "One request per claim from your hospital system, in one shape for every insurer. Spreadsheet, scanned-form and web-form routes are on the way for clinics without developers.",
    visual: <IntakeVisual />,
  },
  {
    title: "We code and check each line",
    text: "Your own codes are matched to a standard list once, and each insurer's code follows from it. Member, diagnosis, price and totals are checked before anything leaves.",
    visual: <CodingVisual />,
  },
  {
    title: "Delivered, retried, on record",
    text: "Claims go out through a durable queue. A timeout is retried only after asking the insurer whether it already has the claim, so nothing is lost or sent twice.",
    visual: <DeliveryVisual />,
  },
];

const guarantees = [
  {
    title: "Never lost",
    text: "A claim and the job that sends it are saved in one transaction. A crash, a restart or a slow insurer cannot drop it.",
  },
  { title: "Never sent twice", text: "Before every retry we ask the insurer for the claim. Idempotency keys stop duplicate requests at the door." },
  { title: "Credentials sealed", text: "Insurer passwords are sealed with AES-256 the moment they are saved. No screen or endpoint returns them." },
  { title: "Every attempt on record", text: "Each claim keeps its full history: checks, sends, timeouts, the insurer's reference and reply." },
  { title: "Built for month-end", text: "Worker pools on a Postgres queue, measured at about 800 claims a second end to end on one 4-core server." },
];

export default function Home() {
  return (
    <div className="lp">
      <Reveal />
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }} />
      <a className="lp-skip" href="#main">
        Skip to content
      </a>

      <header className="lp-nav" id="top">
        <div className="lp-wrap lp-nav__inner">
          <Link href="/" className="lp-nav__brand" aria-label={`${site.name} home`}>
            <Logo tone="reversed" className="lp-nav__logo" />
          </Link>
          <nav aria-label="Main" className="lp-nav__links">
            <a href="#why">Why Payahub</a>
            <a href="#how">How it works</a>
            <a href="#developers">Developers</a>
            <a href="#pricing">Pricing</a>
            <Link href="/docs">Docs</Link>
          </nav>
          <div className="lp-nav__actions">
            <Link href="/signin" className="lp-nav__signin">
              Sign in
            </Link>
            <Link href="/start" className="lp-btn lp-btn--light lp-btn--sm">
              Start free
            </Link>
          </div>
        </div>
      </header>

      <main id="main">
        <section className="lp-hero" aria-labelledby="hero-h">
          <div className="lp-wrap lp-hero__copy">
            <h1 id="hero-h" className="lp-hero__title">
              Every claim, every insurer, one hub.
            </h1>
            <p className="lp-hero__lead">
              {site.name} sends your clinic&apos;s health insurance claims to every insurer you work with in Ghana. It codes
              each line, checks it before it leaves and tracks it to acknowledgement. No portal logins, no claim lost.
            </p>
            <div className="lp-hero__actions">
              <Link href="/start" className="lp-btn lp-btn--primary lp-btn--lg">
                Start your free trial
              </Link>
              <a href="#try" className="lp-btn lp-btn--ghost lp-btn--lg">
                Send a test claim
              </a>
            </div>
            <p className="lp-hero__fine">
              {TRIAL_DAYS} days and {TRIAL_LIVE_CLAIMS} live claims free, no card, sandbox key on sign-up.
            </p>
          </div>
          <div className="lp-wrap lp-hero__visual">
            <ClaimFlow />
          </div>
        </section>

        <section className="lp-strip" aria-labelledby="strip-h">
          <div className="lp-wrap lp-strip__inner">
            <h2 id="strip-h" className="lp-strip__title">
              Connectors ported from integrations already running in Ghanaian clinics
            </h2>
            <ul className="lp-strip__names">
              {platforms.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </div>
        </section>

        <section id="why" className="lp-section lp-why" aria-labelledby="why-h">
          <div className="lp-wrap lp-problem-story">
            <div data-reveal>
              <h2 id="why-h" className="lp-h2">
                Every insurer now wants its own platform.
              </h2>
              <div className="lp-prose lp-lead">
                <p>
                  For years, a few aggregators carried most of Ghana&apos;s private insurers. A clinic learned one
                  system and reached many payers through it.
                </p>
                <p>
                  That is changing. Insurers want control of their own pricing, billing and turnaround, and modern
                  tools, AI included, have made the submission logic that once took years to build something an
                  insurer can now build itself. Platforms such as SmartClaims and ClaimIQ show the direction, and
                  more are coming.
                </p>
                <p>
                  Insurers clear claims faster on their own platforms. <strong>The cost lands on the clinic.</strong>{" "}
                  The billing desk that sees the patient now keeps a login, a form, a code list and a set of rules for
                  every payer, and re-keys the same visit into each one.
                </p>
              </div>
            </div>
            <figure className="lp-desk" data-reveal>
              <h3>One clinic&apos;s claims, on an ordinary day</h3>
              <p>Each payer route brings its own login, codes and rules.</p>
              <table>
                <thead>
                  <tr>
                    <th scope="col">Route</th>
                    <th scope="col">What the desk keeps for it</th>
                  </tr>
                </thead>
                <tbody>
                  {platforms.map((p) => (
                    <tr key={p}>
                      <td>{p}</td>
                      <td>Its own login, form and code list</td>
                    </tr>
                  ))}
                  <tr>
                    <td>An insurer&apos;s own system</td>
                    <td>Another login, another set of rules</td>
                  </tr>
                </tbody>
                <tfoot>
                  <tr>
                    <td>With {site.name}</td>
                    <td>One request, one code list</td>
                  </tr>
                </tfoot>
              </table>
              <figcaption className="sr-only">
                A clinic working with five to seven payers keeps a separate login, form and code list for each.
              </figcaption>
            </figure>
          </div>
        </section>

        <section className="lp-section lp-problem" aria-labelledby="problem-h">
          <div className="lp-wrap">
            <div className="lp-head" data-reveal>
              <h2 id="problem-h" className="lp-h2">
                Month-end should not take a month.
              </h2>
            </div>
            <div className="lp-compare">
              <article className="lp-compare__card lp-compare__card--before" data-reveal>
                <h3>Today</h3>
                <ul>
                  <li>A different portal, login and form for each insurer</li>
                  <li>Codes retyped by hand from the price list</li>
                  <li>Spreadsheets and paper sent in batches at month-end</li>
                  <li>Rejections weeks later over one wrong code</li>
                  <li>No way to tell whether a claim even arrived</li>
                </ul>
              </article>
              <article className="lp-compare__card lp-compare__card--after" data-reveal>
                <h3>With {site.name}</h3>
                <ul>
                  <li>One request per claim, for every insurer</li>
                  <li>Codes mapped once, then applied for every insurer</li>
                  <li>Each claim sent the moment it is ready</li>
                  <li>Checked against the insurer&apos;s rules before it leaves</li>
                  <li>Every claim tracked to the insurer&apos;s acknowledgement</li>
                </ul>
              </article>
            </div>
          </div>
        </section>

        <section className="lp-section lp-answer lp-dark" aria-labelledby="answer-h">
          <div className="lp-wrap lp-split">
            <div className="lp-split__copy" data-reveal>
              <h2 id="answer-h" className="lp-h2">
                An insurer moves. You change nothing.
              </h2>
              <div className="lp-prose lp-lead">
                <p>
                  We saw this first-hand running claims at one of Ghana&apos;s main private hospitals. So we built the
                  layer we wanted then: one simple, smart interface for the clinic, and every platform behind it.
                </p>
                <p>
                  Your software, or your billing staff in the console, send each claim once. {site.name} translates it
                  for whichever platform the insurer uses. When a new platform appears we add it, and when an insurer
                  switches platforms, re-routing its claims is our work, not a change request to your team.
                </p>
              </div>
            </div>
            <div className="lp-split__visual" data-reveal>
              <SwitchVisual />
            </div>
          </div>
        </section>

        <section id="how" className="lp-section lp-how" aria-labelledby="how-h">
          <div className="lp-wrap">
            <div className="lp-head" data-reveal>
              <h2 id="how-h" className="lp-h2">
                From your bill to the insurer&apos;s reply.
              </h2>
            </div>
            <ol className="lp-steps">
              {steps.map((s) => (
                <li key={s.title} className="lp-step" data-reveal>
                  <div className="lp-step__visual">{s.visual}</div>
                  <h3 className="lp-step__title">{s.title}</h3>
                  <p className="lp-step__text">{s.text}</p>
                </li>
              ))}
            </ol>
          </div>
        </section>

        <section className="lp-section lp-ai lp-dark" aria-labelledby="ai-h">
          <div className="lp-wrap lp-split">
            <div className="lp-split__copy" data-reveal>
              <h2 id="ai-h" className="lp-h2">
                AI matches. Rules decide what is sent.
              </h2>
              <p className="lp-lead">
                Matching a clinic&apos;s line to an insurer&apos;s code is the slow part of every claim. {site.name}{" "}
                proposes each match with its confidence and the evidence for it, and nothing a model writes ever goes
                to an insurer.
              </p>
              <ul className="lp-ticks">
                <li>Every request to an insurer is built by rules from checked fields</li>
                <li>Anything under the confidence bar waits for a person</li>
                <li>Each decision keeps its evidence, for audit and for disputes</li>
              </ul>
              <p className="lp-note">
                Code matching works today. Model-backed matching and reading scanned claim forms are being built on
                the same rule.
              </p>
            </div>
            <div className="lp-split__visual" data-reveal>
              <ReviewVisual />
            </div>
          </div>
        </section>

        <section id="developers" className="lp-section lp-dev" aria-labelledby="dev-h">
          <div className="lp-wrap lp-split lp-split--code">
            <div className="lp-split__copy" data-reveal>
              <h2 id="dev-h" className="lp-h2">
                One request per claim. Any insurer.
              </h2>
              <p className="lp-lead">
                Send the claim as your system holds it: one shape for every insurer, money in pesewas, and an
                idempotency key so a retried request never makes a second claim. We work out what each insurer
                needs, check it and send it.
              </p>
              <div className="lp-dev__links">
                <Link href="/docs" className="lp-btn lp-btn--dark">
                  Read the API reference
                </Link>
                <Link href="/start" className="lp-link">
                  Get a sandbox key
                </Link>
              </div>
            </div>
            <div className="lp-code" data-reveal data-ui>
              <p className="lp-code__bar">Create a claim, and the reply</p>
              <pre>
                <code>{curl}</code>
              </pre>
              <pre className="lp-code__reply">
                <code>{reply}</code>
              </pre>
            </div>
          </div>
        </section>

        <section id="platforms" className="lp-section lp-guide" aria-labelledby="guide-h">
          <div className="lp-wrap lp-split">
            <div className="lp-split__copy" data-reveal>
              <h2 id="guide-h" className="lp-h2">
                Run a claims platform? Connect once.
              </h2>
              <div className="lp-prose lp-lead">
                <p>
                  We are writing an open integration guide for insurers and platform builders: the few interfaces a
                  platform exposes so that every clinic on {site.name} can verify members and submit claims to it,
                  with no new work at the clinic.
                </p>
                <p>
                  We want it to become the common reference for anyone building in Ghana&apos;s claims market. The
                  guide is in draft; write to us to review it early or to have your platform connected.
                </p>
              </div>
              <div className="lp-dev__links">
                <a href="#contact-platform" className="lp-btn lp-btn--dark">
                  Connect your platform
                </a>
                <a href="#contact-specs" className="lp-link">
                  Get the interface
                </a>
              </div>
            </div>
            <ul className="lp-guide__verbs" data-reveal aria-label="What the integration guide covers">
              <li>
                <strong>Check a member</strong>
                <span>Is this member covered today, and on which plan?</span>
              </li>
              <li>
                <strong>Submit a claim</strong>
                <span>Whole, or line by line, with diagnoses and the clinic&apos;s own reference.</span>
              </li>
              <li>
                <strong>Find a claim by our reference</strong>
                <span>So a retry after a timeout never makes a second claim.</span>
              </li>
              <li>
                <strong>Report the outcome</strong>
                <span>Acknowledged, queried or rejected, with the reason.</span>
              </li>
              <li>
                <strong>Publish your code list</strong>
                <span>Services and medicines, so clinics&apos; items map to yours automatically.</span>
              </li>
            </ul>
          </div>
        </section>

        <section id="try" className="lp-section lp-try lp-dark field-navy" aria-labelledby="try-h">
          <div className="lp-wrap lp-split">
            <div className="lp-split__copy" data-reveal>
              <h2 id="try-h" className="lp-h2">
                Send a test claim.
              </h2>
              <p className="lp-lead">
                Type a member number. This runs the same rules as the sandbox your trial gets, failures included: a
                refusal, a timeout that is retried, and a reply lost on the way back.
              </p>
            </div>
            <div data-reveal>
              <SandboxTry />
            </div>
          </div>
        </section>

        <section className="lp-section lp-guarantees" aria-labelledby="g-h">
          <div className="lp-wrap">
            <div className="lp-head" data-reveal>
              <h2 id="g-h" className="lp-h2">
                A claim is revenue. We treat it that way.
              </h2>
            </div>
            <div className="lp-promise">
              <div className="lp-promise__lead" data-reveal>
                <h3>{guarantees[0].title}</h3>
                <p>{guarantees[0].text}</p>
              </div>
              <ul className="lp-promise__list">
                {guarantees.slice(1).map((g) => (
                  <li key={g.title} data-reveal>
                    <h3>{g.title}</h3>
                    <p>{g.text}</p>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </section>

        <section className="lp-section lp-console" aria-labelledby="console-h">
          <div className="lp-wrap">
            <div className="lp-head" data-reveal>
              <h2 id="console-h" className="lp-h2">
                Every claim, on one screen.
              </h2>
            </div>
            <div className="lp-shots">
              <figure className="lp-shot lp-shot--main" data-reveal>
                <Image
                  src="/console/claims-list.png"
                  alt="The claims list: five sandbox claims, four acknowledged and one rejected, each with its amount and the insurer's claim number."
                  width={1888}
                  height={1126}
                  sizes="(max-width: 64em) 100vw, 62vw"
                />
                <figcaption>Every claim, its state and the insurer&apos;s number.</figcaption>
              </figure>
              <figure className="lp-shot" data-reveal>
                <Image
                  src="/console/audit-trail.png"
                  alt="A claim's history: received, queued, a timed-out attempt, then acknowledged one second later."
                  width={1174}
                  height={604}
                  sizes="(max-width: 64em) 100vw, 34vw"
                />
                <figcaption>A timeout, retried and acknowledged.</figcaption>
              </figure>
              <figure className="lp-shot" data-reveal>
                <Image
                  src="/console/first-claim-steps.png"
                  alt="The onboarding steps, all ticked: account, how you bill, insurers, a first claim, plan."
                  width={1304}
                  height={864}
                  sizes="(max-width: 64em) 100vw, 34vw"
                />
                <figcaption>Guided from sign-up to a live claim.</figcaption>
              </figure>
            </div>
          </div>
        </section>

        <section id="pricing" className="lp-section lp-pricing" aria-labelledby="pricing-h">
          <div className="lp-wrap">
            <div className="lp-head" data-reveal>
              <h2 id="pricing-h" className="lp-h2">
                Pay only for claims an insurer acknowledges.
              </h2>
            </div>
            <div className="lp-pricing__box" data-reveal>
              <Pricing />
            </div>
          </div>
        </section>

        <section id="contact" className="lp-section lp-contact" aria-labelledby="contact-h">
          <div className="lp-wrap lp-split lp-split--form">
            <div className="lp-split__copy" data-reveal>
              <h2 id="contact-h" className="lp-h2">
                Tell us what you need.
              </h2>
              <div className="lp-prose lp-lead">
                <p>
                  A clinic ready to stop re-keying claims, an insurer or platform we have not connected yet, a payer
                  you want us to reach, or a developer who wants the interface: pick one and we will take it from
                  there.
                </p>
                <p>
                  Prefer email? Write to <a href={`mailto:${site.email}`}>{site.email}</a>.
                </p>
              </div>
            </div>
            <div data-reveal>
              <LeadForm />
            </div>
          </div>
        </section>

        <section className="lp-section lp-faq" aria-labelledby="faq-h">
          <div className="lp-wrap lp-faq__grid">
            <div className="lp-head" data-reveal>
              <h2 id="faq-h" className="lp-h2">
                What clinics ask first.
              </h2>
              <p className="lp-lead">
                Anything else? Write to <a href={`mailto:${site.email}`}>{site.email}</a>.
              </p>
            </div>
            <div className="lp-faq__list">
              {faqs.map((f, i) => (
                <details key={f.q} className="lp-faq__item" open={i === 0}>
                  <summary>
                    <h3>{f.q}</h3>
                  </summary>
                  <p>{f.a}</p>
                </details>
              ))}
            </div>
          </div>
        </section>

        <section className="lp-cta lp-dark" aria-labelledby="cta-h">
          <div className="lp-wrap lp-cta__inner">
            <h2 id="cta-h" className="lp-cta__title">
              Your first claim, today.
            </h2>
            <p className="lp-lead">
              Your sandbox key is ready the moment you sign up. {TRIAL_DAYS} days and {TRIAL_LIVE_CLAIMS} live claims
              free, no card, no sales call.
            </p>
            <div className="lp-hero__actions">
              <Link href="/start" className="lp-btn lp-btn--primary lp-btn--lg">
                Start your free trial
              </Link>
              <Link href="/docs" className="lp-btn lp-btn--ghost lp-btn--lg">
                Read the docs
              </Link>
            </div>
          </div>
        </section>
      </main>

      <footer className="lp-footer">
        <div className="lp-wrap lp-footer__grid">
          <div className="lp-footer__brand">
            <Logo tone="reversed" className="lp-footer__logo" />
            <p>One hub for insurance claims, built in {site.city}.</p>
          </div>
          <nav aria-label="Product" className="lp-footer__col">
            <h2>Product</h2>
            <a href="#how">How it works</a>
            <a href="#pricing">Pricing</a>
            <Link href="/start">Start free trial</Link>
            <Link href="/signin">Sign in</Link>
          </nav>
          <nav aria-label="Developers" className="lp-footer__col">
            <h2>Developers</h2>
            <Link href="/docs">API reference</Link>
            <a href="#platforms">For insurers and platforms</a>
            <a href="#contact-specs">Interface specs</a>
            <a href="#try">Sandbox</a>
          </nav>
          <div className="lp-footer__col">
            <h2>Contact</h2>
            <a href="#contact">Talk to us</a>
            <a href={`mailto:${site.email}`}>{site.email}</a>
            <span>{site.city}, Ghana</span>
          </div>
        </div>
        <div className="lp-wrap lp-footer__base">
          <span>
            © {new Date().getFullYear()} {site.name}
          </span>
          <a href="#top">Back to top</a>
        </div>
      </footer>
    </div>
  );
}
