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
          {/* The small symbol: below 48px the spider keeps a hub dot, not the arrows. */}
          <img className="app-brand__mark" src="/brand/payahub-symbol-small-color.svg" alt="" width={30} height={30} />
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
