import type { Metadata } from "next";
import Link from "next/link";

import { site } from "@/app/site";
import { SignUpForm } from "@/components/forms";
import { TRIAL_DAYS, TRIAL_LIVE_CLAIMS } from "@/lib/pricing";
import { Logo } from "@/components/logo";

export const metadata: Metadata = {
  title: "Start your free trial",
  description: `Start a ${TRIAL_DAYS}-day ${site.name} trial: a sandbox API key at once, ${TRIAL_LIVE_CLAIMS} live claims, no card.`,
  alternates: { canonical: "/start" },
};

export default function Start() {
  return (
    <div className="auth">
      <header className="nav wrap">
        <Link href="/" className="nav__brand" aria-label={`${site.name} home`}>
          <Logo />
        </Link>
        <nav aria-label="Account" className="nav__links nav__links--always">
          <Link href="/signin">Sign in</Link>
        </nav>
      </header>
      <main className="auth__grid wrap">
        <div className="auth__copy">
          <h1 className="h-head">Start your free trial</h1>
          <ul className="auth__gets">
            <li>A sandbox API key the moment you finish this form.</li>
            <li>{TRIAL_DAYS} days with every insurer we support, and {TRIAL_LIVE_CLAIMS} live claims.</li>
            <li>No card and no sales call. Choose a plan only when you are ready.</li>
          </ul>
        </div>
        <SignUpForm />
      </main>
    </div>
  );
}
