import type { Metadata } from "next";
import Link from "next/link";

import { site } from "@/app/site";
import { SignInForm } from "@/components/forms";

export const metadata: Metadata = { title: "Sign in", alternates: { canonical: "/signin" } };

export default function SignIn() {
  return (
    <div className="auth">
      <header className="nav wrap">
        <Link href="/" className="wordmark">
          {site.name}
        </Link>
        <nav aria-label="Account" className="nav__links nav__links--always">
          <Link href="/start">Start free trial</Link>
        </nav>
      </header>
      <main className="auth__grid wrap">
        <div className="auth__copy">
          <h1 className="h-head">Sign in</h1>
          <p className="auth__lead">Your insurers, keys and claims are where you left them.</p>
        </div>
        <SignInForm />
      </main>
    </div>
  );
}
