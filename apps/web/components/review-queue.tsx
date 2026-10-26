"use client";

import Link from "next/link";
import { useActionState } from "react";

import { confirmIssues, mapHeldCode, type FormState } from "@/app/actions";
import { Messages, Submit } from "@/components/forms";
import { RemoteSelect, TextInput } from "@/components/ui";
import { kindName, money } from "@/lib/format";
import type { Suggestion } from "@/lib/types";

import type { Issue } from "@/components/claim-checks";

export type HeldCode = {
  code: string;
  name: string;
  claims: number;
  lines: number;
  amount: number;
  currency: string;
  oldest_claim_at: string;
  suggestions: Suggestion[];
};

const pct = (n: number) => `${Math.round(n * 100)}%`;

// HeldCodeRow maps one of the clinic's own codes to a standard item; every
// claim waiting on the code is checked again when it is saved.
export function HeldCodeRow({
  h,
  env,
  canMap,
}: {
  h: HeldCode;
  env: string;
  canMap: boolean;
}) {
  const [s, action] = useActionState(mapHeldCode, {} as FormState);
  const top = h.suggestions[0];
  return (
    <li className="app-held">
      <div className="app-held__what">
        <span className="app-strong">{h.name || h.code}</span>
        <span className="app-muted app-block">
          Your code <span className="app-id">{h.code}</span>
        </span>
      </div>
      <div className="app-held__count">
        <span className="app-strong">
          {h.claims} claim{h.claims === 1 ? "" : "s"}
        </span>
        <span className="app-muted app-block">
          {money(h.amount, h.currency)} held
        </span>
      </div>
      {canMap ? (
        <form action={action} className="app-held__map">
          <input type="hidden" name="code" value={h.code} />
          <input type="hidden" name="name" value={h.name} />
          <input type="hidden" name="env" value={env} />
          <RemoteSelect
            id={`held-${h.code}`}
            name="item"
            source="items"
            label="Standard item"
            placeholder="Search the standard list"
            defaultOption={
              top
                ? {
                    value: top.item.id,
                    label: top.item.name,
                    description: `${kindName[top.item.kind]} · ${pct(top.confidence)} match by its words`,
                  }
                : null
            }
            description={
              top
                ? `Suggested: ${pct(top.confidence)} match. Check it is the same thing.`
                : "No close match. Search the standard list."
            }
          />
          <Submit className="app-button app-button--primary">Map</Submit>
          <Messages state={s} />
        </form>
      ) : (
        <p className="app-muted app-held__map">
          Someone who manages setup can map this code.
        </p>
      )}
    </li>
  );
}

export type WaitingClaim = {
  claim: {
    id: string;
    payer_id: string;
    total: number;
    currency: string;
    member: { member_number: string };
    encounter: { start_date: string };
    lines: {
      description: string;
      mapping?: {
        source?: { value: string };
        target?: { value: string; display?: string };
        evidence?: {
          origin: string;
          confidence?: number;
          assertion?: string;
          model_version?: string;
        };
      };
    }[];
    diagnoses: {
      code: { value: string };
      mapping?: {
        target?: { value: string; display?: string };
        evidence?: { confidence?: number; assertion?: string };
      };
    }[];
  };
  issues: Issue[];
};

const origin: Record<string, string> = {
  ai_mapped: "matched by a model",
  ai_extracted: "read from the form by a model",
};

// What the issue is about, in the claim's own words: the line and the code
// a model proposed for it.
function Subject({ w, i }: { w: WaitingClaim; i: Issue }) {
  const line = /^lines\[(\d+)\]/.exec(i.field ?? "");
  const dx = /^diagnoses\[(\d+)\]/.exec(i.field ?? "");
  if (line) {
    const l = w.claim.lines[Number(line[1])];
    const m = l?.mapping;
    if (!l) return null;
    const e = m?.evidence;
    return (
      <span className="app-block">
        <span className="app-strong">{l.description}</span>
        {m?.target && (
          <>
            {" "}
            sent as {m.target.display ?? ""}{" "}
            <span className="app-id">{m.target.value}</span>
          </>
        )}
        {e && (
          <span className="app-muted app-block">
            {[
              origin[e.origin] ?? e.origin,
              e.confidence != null ? `${pct(e.confidence)} sure` : null,
              e.assertion ? `“${e.assertion}”` : null,
            ]
              .filter(Boolean)
              .join(", ")}
          </span>
        )}
      </span>
    );
  }
  if (dx) {
    const d = w.claim.diagnoses[Number(dx[1])];
    if (!d) return null;
    return (
      <span className="app-block">
        <span className="app-strong">Diagnosis {d.code.value}</span>
        {d.mapping?.target && (
          <>
            {" "}
            sent as <span className="app-id">{d.mapping.target.value}</span>
          </>
        )}
        {d.mapping?.evidence?.confidence != null && (
          <span className="app-muted app-block">
            {pct(d.mapping.evidence.confidence)} sure
          </span>
        )}
      </span>
    );
  }
  const dup = i.field?.startsWith("claim:") ? i.field.slice(6) : null;
  return dup ? (
    <span className="app-block">
      <span className="app-strong">Possible copy</span> of{" "}
      <Link href={`/console/claims/${dup}`}>another claim</Link>
    </span>
  ) : null;
}

// WaitingCard is one claim stopped for a person: each thing to check, with
// a note, confirmed in one go.
export function WaitingCard({
  w,
  payer,
  canConfirm,
}: {
  w: WaitingClaim;
  payer: string;
  canConfirm: boolean;
}) {
  const [s, action] = useActionState(confirmIssues, {} as FormState);
  const c = w.claim;
  return (
    <li className="app-waiting">
      <div className="app-waiting__head">
        <Link href={`/console/claims/${c.id}`} className="app-strong">
          {c.member.member_number}, {money(c.total, c.currency)}
        </Link>
        <span className="app-muted">
          {payer} · visit {c.encounter.start_date}
        </span>
      </div>
      <form action={action} className="app-form app-review">
        <input type="hidden" name="id" value={c.id} />
        <fieldset className="app-fieldset">
          <legend className="sr-only">Check these against the source</legend>
          {w.issues.map((i, n) => (
            <label key={n} className="app-issue app-issue--review">
              <input
                type="checkbox"
                name="confirm"
                value={`${i.code}|${i.field ?? ""}`}
                disabled={!canConfirm}
              />
              <span>
                <Subject w={w} i={i} />
                <span className="app-muted app-block">{i.message}</span>
              </span>
            </label>
          ))}
        </fieldset>
        {canConfirm && (
          <div className="app-waiting__foot">
            <TextInput
              id={`note-${c.id}`}
              name="note"
              label="Note (optional)"
              maxLength={500}
              placeholder="What you checked"
              className="app-grow"
            />
            <Submit className="app-button">Confirm what I checked</Submit>
          </div>
        )}
        <Messages state={s} />
      </form>
    </li>
  );
}
