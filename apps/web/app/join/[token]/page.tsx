import type { Metadata } from "next";
import Link from "next/link";

import { site } from "@/app/site";
import { Logo } from "@/components/logo";
import { JoinForm } from "@/components/team-forms";
import { api } from "@/lib/api";
import { date } from "@/lib/format";
import { roleName, roles } from "@/lib/roles";
import type { InvitationView } from "@/lib/types";

export const metadata: Metadata = {
  title: "Join your team",
  robots: { index: false, follow: false },
};

export default async function Join({
  params,
}: {
  params: Promise<{ token: string }>;
}) {
  const { token } = await params;
  const res = await api<InvitationView>(
    `/v1/invitations/accept/${encodeURIComponent(token)}`,
    { token: null },
  );
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
        {res.ok ? (
          <>
            <div className="auth__copy">
              <h1 className="h-head">Join {res.data.organization}</h1>
              <p className="auth__lead">
                {res.data.invited_by} invited you to join the team.
              </p>
              <p>
                You will sign in as <strong>{res.data.email}</strong> with the
                role <strong>{roleName[res.data.role]}</strong>:{" "}
                {roles.find((r) => r.id === res.data.role)?.does}
              </p>
              <p className="auth__alt">
                This link works until {date(res.data.expires_at)}.
              </p>
            </div>
            <JoinForm token={token} name={res.data.name} />
          </>
        ) : (
          <div className="auth__copy">
            <h1 className="h-head">This invitation has run out</h1>
            <p className="auth__lead">
              It has expired, been withdrawn or already been used. Ask whoever
              invited you to send a new one.
            </p>
            <p>
              <Link href="/signin" className="button">
                Sign in instead
              </Link>
            </p>
          </div>
        )}
      </main>
    </div>
  );
}
