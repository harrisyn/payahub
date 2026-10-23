"use client";

import Link from "next/link";
import { useActionState } from "react";

import {
  uploadClaims,
  type ClaimUploadState,
  type ImportedClaim,
} from "@/app/actions";
import { Messages, Submit } from "@/components/forms";
import { money } from "@/lib/format";

function status(c: ImportedClaim): [string, string] {
  if (c.queued) return ["Sent", "app-pill--good"];
  if (c.errors.length) return ["Cannot be read", "app-pill--bad"];
  if (c.state === "ready")
    return [c.claim_id ? "Saved, ready" : "Ready", "app-pill--good"];
  if (c.state === "needs_review") return ["Needs review", "app-pill--wait"];
  return [
    c.claim_id ? "Saved, needs fixing" : "Needs fixing",
    "app-pill--wait",
  ];
}

export function ClaimUploadForm({
  payerNames,
}: {
  payerNames: Record<string, string>;
}) {
  const [s, action] = useActionState(uploadClaims, {} as ClaimUploadState);
  const claims = s.claims ?? [];
  const ready = claims.filter(
    (c) => !c.errors.length && c.state === "ready",
  ).length;
  const readable = claims.filter((c) => !c.errors.length).length;
  return (
    <div className="app-stack">
      <form action={action} className="app-inline-form" noValidate>
        <div className="field field--inline">
          <label htmlFor="f-upload">Claim lines (CSV or Excel)</label>
          <input
            id="f-upload"
            name="upload"
            type="file"
            accept=".csv,.xlsx,text/csv"
          />
        </div>
        <input type="hidden" name="intent" value="check" />
        <Submit className="app-button">Check the file</Submit>
      </form>
      <Messages state={s} />
      {claims.length > 0 && (
        <>
          {!s.saved && (
            <p className="app-muted">
              {claims.length} {claims.length === 1 ? "claim" : "claims"} in the
              file: {ready} ready to send, {readable - ready} to fix after
              saving, {claims.length - readable} that cannot be read.
            </p>
          )}
          <table className="app-table app-upload">
            <thead>
              <tr>
                <th scope="col">Claim</th>
                <th scope="col">Payer and member</th>
                <th scope="col" className="app-right">
                  Total
                </th>
                <th scope="col">Result</th>
              </tr>
            </thead>
            <tbody>
              {claims.map((c) => {
                const [label, tone] = status(c);
                const notes = [
                  ...c.errors.map((e) => `${e.field}: ${e.message}`),
                  ...c.issues
                    .filter(
                      (i) => i.severity !== "warning" || c.state !== "ready",
                    )
                    .map((i) => i.message),
                ];
                return (
                  <tr key={c.reference}>
                    <td>
                      {c.claim_id ? (
                        <Link
                          href={`/console/claims/${c.claim_id}`}
                          className="app-strong-link"
                        >
                          {c.reference}
                        </Link>
                      ) : (
                        <span className="app-strong-link">{c.reference}</span>
                      )}
                      <span className="app-muted app-block">
                        {c.rows.length === 1
                          ? `Row ${c.rows[0]}`
                          : `Rows ${c.rows[0]}–${c.rows[c.rows.length - 1]}`}{" "}
                        · {c.lines} {c.lines === 1 ? "line" : "lines"}
                      </span>
                    </td>
                    <td>
                      {(c.payer_id && payerNames[c.payer_id]) || c.payer_id || (
                        <span className="app-dash">–</span>
                      )}
                      <span className="app-muted app-block">
                        {c.member_number}
                      </span>
                    </td>
                    <td className="app-right app-nowrap">
                      {c.lines ? (
                        money(c.total, c.currency)
                      ) : (
                        <span className="app-dash">–</span>
                      )}
                    </td>
                    <td>
                      <span className={`app-pill ${tone}`}>{label}</span>
                      {notes.length > 0 && (
                        <ul className="app-notes">
                          {notes.slice(0, 4).map((n) => (
                            <li key={n}>{n}</li>
                          ))}
                        </ul>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {s.file && !s.saved && readable > 0 && (
            <div className="app-form__foot">
              <form action={action}>
                <input type="hidden" name="file" value={s.file} />
                <input type="hidden" name="intent" value="send" />
                <Submit className="app-button app-button--primary">
                  Save {readable} and send the {ready} ready
                </Submit>
              </form>
              <form action={action}>
                <input type="hidden" name="file" value={s.file} />
                <input type="hidden" name="intent" value="save" />
                <Submit className="app-button">Save as drafts only</Submit>
              </form>
            </div>
          )}
        </>
      )}
    </div>
  );
}
