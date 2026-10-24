import Link from "next/link";

import {
  HeldCodeRow,
  WaitingCard,
  type HeldCode,
  type WaitingClaim,
} from "@/components/review-queue";
import { api } from "@/lib/api";
import { can } from "@/lib/roles";
import { requireOrg } from "@/lib/session";
import type { Payer } from "@/lib/types";

export const metadata = { title: "To review" };

const tabs = [
  { env: "", label: "All" },
  { env: "live", label: "Live" },
  { env: "sandbox", label: "Sandbox" },
];

type Queue = {
  codes: HeldCode[];
  claims: WaitingClaim[];
  counts: { needs_review: number; ready: number };
};

export default async function ReviewQueue({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | undefined>>;
}) {
  const { organization: org } = await requireOrg();
  const sp = await searchParams;
  const env = sp.env === "live" || sp.env === "sandbox" ? sp.env : "";
  const n = (k: string) => Number(sp[k] ?? 0) || 0;
  const moved = [
    n("ready") && `${n("ready")} ready to send`,
    n("review") && `${n("review")} waiting for a person`,
    n("fix") && `${n("fix")} with something else to fix`,
  ].filter(Boolean);
  const [q, payers] = await Promise.all([
    api<Queue>(`/v1/review-queue${env ? `?environment=${env}` : ""}`),
    api<{ data: Payer[] }>("/v1/payers"),
  ]);
  const { codes, claims, counts } = q.ok
    ? q.data
    : { codes: [], claims: [], counts: { needs_review: 0, ready: 0 } };
  const payerName = new Map(
    (payers.ok ? payers.data.data : []).map((p) => [p.id, p.name]),
  );
  const held = codes.reduce((n, c) => n + c.claims, 0);
  const perms = org.user.permissions;
  const finding = [
    codes.length &&
      `${codes.length} of your code${codes.length === 1 ? "" : "s"} hold${codes.length === 1 ? "s" : ""} claims back`,
    counts.needs_review &&
      `${counts.needs_review} claim${counts.needs_review === 1 ? " waits" : "s wait"} for a person`,
    counts.ready &&
      `${counts.ready} ${counts.ready === 1 ? "is" : "are"} checked and ready to send`,
  ].filter(Boolean);

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <Link href="/console/claims" className="app-link">
            Claims
          </Link>
          <h1>To review</h1>
          <p className="app-finding">
            {finding.length
              ? `${finding.join("; ")}.`
              : "Nothing is waiting for you. Every unsent claim has what it needs."}
          </p>
        </div>
        {counts.ready > 0 && (
          <Link
            href={`/console/claims?state=ready${env ? `&env=${env}` : ""}`}
            className="app-button"
          >
            See claims ready to send
          </Link>
        )}
      </div>
      {sp.mapped && (
        <p className="app-banner app-banner--good" role="status">
          {sp.mapped} is mapped. {n("claims")} claim
          {n("claims") === 1 ? "" : "s"} checked again
          {moved.length ? `: ${moved.join(", ")}` : ""}.
        </p>
      )}
      <nav className="app-tabs" aria-label="Environment">
        {tabs.map((t) => (
          <Link
            key={t.label}
            href={t.env ? `?env=${t.env}` : "?"}
            aria-current={env === t.env ? "page" : undefined}
          >
            {t.label}
          </Link>
        ))}
      </nav>

      <section className="app-card" aria-labelledby="held-h">
        <div className="app-card__head">
          <div>
            <h2 id="held-h" className="app-card__title">
              Your codes with no standard item
            </h2>
            <p className="app-muted app-measure">
              {codes.length
                ? `These codes are on ${held} unsent claim${held === 1 ? "" : "s"}. Map each once and every claim waiting on it is checked again; the insurer's own code follows from the standard item.`
                : "Every code on your unsent claims is mapped."}
            </p>
          </div>
        </div>
        {codes.length > 0 && (
          <ul className="app-held-list">
            {codes.map((h) => (
              <HeldCodeRow
                key={h.code}
                h={h}
                env={env}
                canMap={can(perms, "setup")}
              />
            ))}
          </ul>
        )}
      </section>

      <section className="app-card" aria-labelledby="wait-h">
        <h2 id="wait-h" className="app-card__title">
          Waiting for a person
        </h2>
        <p className="app-muted app-measure">
          {claims.length
            ? "A model proposed these codes with less confidence than we send on, or the claim looks like a copy of another. Check each against the bill or the form, then confirm."
            : "No claim is waiting for someone to check it."}
        </p>
        {claims.length > 0 && (
          <ul className="app-waiting-list">
            {claims.map((w) => (
              <WaitingCard
                key={w.claim.id}
                w={w}
                payer={payerName.get(w.claim.payer_id) ?? w.claim.payer_id}
                canConfirm={can(perms, "claims")}
              />
            ))}
          </ul>
        )}
        {counts.needs_review > claims.length && (
          <p className="app-muted">
            Showing the oldest {claims.length} of {counts.needs_review}.
          </p>
        )}
      </section>
    </div>
  );
}
