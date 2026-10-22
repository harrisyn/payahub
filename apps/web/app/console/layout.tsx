import type { Metadata } from "next";
import Link from "next/link";
import type { ReactNode } from "react";

import { signOut } from "@/app/actions";
import { site } from "@/app/site";
import { ConsoleNav } from "@/components/console-nav";
import { daysLeft, planName } from "@/lib/format";
import { requireOrg } from "@/lib/session";

export const metadata: Metadata = { title: "Console", robots: { index: false, follow: false } };

export default async function ConsoleLayout({ children }: { children: ReactNode }) {
  const { organization: org, usage } = await requireOrg();
  const trial = org.plan === "trial" || org.plan === "enterprise_requested";
  return (
    <div className="app">
      <aside className="app-side">
        <Link href="/console" className="app-brand">
          <svg className="app-brand__mark" viewBox="0 0 32 32" aria-hidden="true">
            <rect width="32" height="32" rx="7" fill="#6bb4ee" />
            <path d="M9 9h10a6 6 0 0 1 0 12h-6v4H9z" fill="#0e1822" />
            <path d="M13 13h5.5a2 2 0 0 1 0 4H13z" fill="#ff5a1f" />
          </svg>
          <span>
            <span className="app-brand__name">{site.name}</span>
            <span className="app-brand__org">{org.name}</span>
          </span>
        </Link>
        <ConsoleNav />
        <p className="app-side__foot">
          <Link href="/docs">API docs</Link>
        </p>
      </aside>
      <header className="app-top">
        <p className="app-top__status">
          {trial ? (
            <>
              {planName[org.plan]}: <span className="num">{daysLeft(org.trial_ends_at)}</span> days and{" "}
              <span className="num">{Math.max(0, usage.trial_live_limit - usage.live_claims)}</span> live claims left
            </>
          ) : (
            <>{planName[org.plan]} plan</>
          )}
        </p>
        <form action={signOut} className="app-top__account">
          <span className="app-top__user">{org.user.name}</span>
          <button type="submit" className="app-button app-button--quiet">
            Sign out
          </button>
        </form>
      </header>
      <main className="app-sheet">{children}</main>
    </div>
  );
}
