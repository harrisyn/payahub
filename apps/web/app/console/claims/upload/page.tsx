import Link from "next/link";

import { ClaimUploadForm } from "@/components/claim-upload";
import { api } from "@/lib/api";
import { requireOrg } from "@/lib/session";
import type { Payer } from "@/lib/types";

export const metadata = { title: "Upload claims" };

export default async function UploadClaims() {
  await requireOrg();
  const payers = await api<{ data: Payer[] }>("/v1/payers");
  const payerNames = Object.fromEntries((payers.ok ? payers.data.data : []).map((p) => [p.id, p.name]));
  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/claims">Claims</Link>
          </p>
          <h1>Upload claims</h1>
          <p className="app-finding">
            One row per claim line; rows with the same claim number make one claim. Every claim is checked before
            anything is saved, and uploading the same file twice creates nothing new.
          </p>
        </div>
        <a href="/templates/claims-template.csv" className="app-button" download>
          Download the template
        </a>
      </div>
      <div className="app-split">
        <section className="app-card" aria-labelledby="up-h">
          <h2 id="up-h" className="app-card__title">
            Your file
          </h2>
          <ClaimUploadForm payerNames={payerNames} />
        </section>
        <section className="app-card" aria-labelledby="cols-h">
          <h2 id="cols-h" className="app-card__title">
            Columns we read
          </h2>
          <dl className="app-dl">
            <div>
              <dt>Claim No</dt>
              <dd>Groups rows into one claim. Required.</dd>
            </div>
            <div>
              <dt>Payer</dt>
              <dd>The insurer&apos;s name or id, on the claim&apos;s first row.</dd>
            </div>
            <div>
              <dt>Member No</dt>
              <dd>The member&apos;s card number. Patient name is optional.</dd>
            </div>
            <div>
              <dt>Visit Date</dt>
              <dd>21/10/2026, 2026-10-21 or an Excel date.</dd>
            </div>
            <div>
              <dt>Diagnosis</dt>
              <dd>ICD-10 codes, the primary first, separated by semicolons.</dd>
            </div>
            <div>
              <dt>Item Code</dt>
              <dd>Your code or a standard code. With your price list, price and description can be left blank.</dd>
            </div>
            <div>
              <dt>Qty, Unit Price</dt>
              <dd>Quantity defaults to 1. Prices in cedis.</dd>
            </div>
          </dl>
        </section>
      </div>
    </div>
  );
}
