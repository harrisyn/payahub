import Link from "next/link";

import { ClaimReader } from "@/components/claim-read";
import { api } from "@/lib/api";
import { requireOrg } from "@/lib/session";
import type { Payer } from "@/lib/types";

export const metadata = { title: "Read a claim form" };

export default async function ReadClaim() {
  await requireOrg();
  const payers = await api<{ data: Payer[] }>("/v1/payers");
  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/claims">Claims</Link>
          </p>
          <h1>Read a claim form</h1>
          <p className="app-finding">
            Upload a scan or photo of a paper claim form. A model reads it into
            a claim; you check what it marks, then save. Nothing goes to an
            insurer from the reading itself.
          </p>
        </div>
      </div>
      <ClaimReader payers={payers.ok ? payers.data.data : []} />
    </div>
  );
}
