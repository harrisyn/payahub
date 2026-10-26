import Link from "next/link";

import { sendTestClaim } from "@/app/actions";
import { ClaimsTable } from "@/components/claim-row";
import { Submit } from "@/components/forms";
import { api } from "@/lib/api";
import { stateLabel } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { ClaimState, ClaimSummary, Payer } from "@/lib/types";

export const metadata = { title: "Claims" };

const tabs = [
  { env: "", label: "All" },
  { env: "live", label: "Live" },
  { env: "sandbox", label: "Sandbox" },
];

export default async function Claims({
  searchParams,
}: {
  searchParams: Promise<{ env?: string; before?: string; state?: string }>;
}) {
  const { usage } = await requireOrg();
  const { env = "", before, state = "" } = await searchParams;
  const shown = state in stateLabel ? (state as ClaimState) : "";
  const q = new URLSearchParams({ limit: "25" });
  if (env) q.set("environment", env);
  if (shown) q.set("state", shown);
  if (before) q.set("before", before);
  const [claims, payers] = await Promise.all([
    api<{ data: ClaimSummary[]; next_before?: string }>(`/v1/claims?${q}`),
    api<{ data: Payer[] }>("/v1/payers"),
  ]);
  const rows = claims.ok ? claims.data.data : [];
  const payerNames = Object.fromEntries(
    (payers.ok ? payers.data.data : []).map((m) => [m.id, m.name]),
  );
  const count = (s: ClaimState) => rows.filter((r) => r.state === s).length;
  const [waiting, ready, review, rejected] = (
    ["submitted", "ready", "needs_review", "rejected"] as const
  ).map(count);

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Claims</h1>
          <p className="app-finding">
            {rows.length === 0
              ? "No claims yet."
              : shown
                ? `${rows.length} ${stateLabel[shown].toLowerCase()} shown.`
                : `${rows.length} shown${waiting ? `, ${waiting} on the way to the insurer` : ""}${ready ? `, ${ready} ready to send` : ""}${review ? `, ${review} waiting for a person` : ""}${rejected ? `, ${rejected} rejected and waiting for a fix` : ""}.`}
          </p>
        </div>
        <div className="app-actions">
          <Link href="/console/claims/review" className="app-button">
            To review
          </Link>
          <Link href="/console/claims/member" className="app-button">
            Check a member
          </Link>
          <Link href="/console/claims/read" className="app-button">
            Read a claim form
          </Link>
          <Link href="/console/claims/upload" className="app-button">
            Upload claims
          </Link>
          <form action={sendTestClaim}>
            <input type="hidden" name="n" value={usage.sandbox_claims} />
            <Submit className="app-button app-button--primary">
              Send a test claim
            </Submit>
          </form>
        </div>
      </div>
      <nav className="app-tabs" aria-label="Environment">
        {tabs.map((t) => (
          <Link
            key={t.label}
            href={`?${new URLSearchParams({ ...(t.env ? { env: t.env } : {}), ...(shown ? { state: shown } : {}) })}`}
            aria-current={env === t.env ? "page" : undefined}
          >
            {t.label}
          </Link>
        ))}
      </nav>
      {shown && (
        <p className="app-muted">
          Only {stateLabel[shown].toLowerCase()} claims.{" "}
          <Link className="app-link" href={env ? `?env=${env}` : "?"}>
            Show every claim
          </Link>
        </p>
      )}
      <section className="app-card app-card--flush" aria-label="Claims">
        {rows.length ? (
          <ClaimsTable rows={rows} payerNames={payerNames} />
        ) : (
          <p className="app-empty">
            Nothing here yet. A test claim goes to the sandbox insurer and shows
            every step it takes.
          </p>
        )}
      </section>
      {claims.ok && claims.data.next_before && (
        <p>
          <Link
            className="app-link"
            href={`?${new URLSearchParams({ ...(env ? { env } : {}), ...(shown ? { state: shown } : {}), before: claims.data.next_before })}`}
          >
            Older claims
          </Link>
        </p>
      )}
    </div>
  );
}
