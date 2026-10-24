import Link from "next/link";

import { MemberCheckForm } from "@/components/member-check";
import { api } from "@/lib/api";
import { requireOrg } from "@/lib/session";
import type { Payer } from "@/lib/types";

export const metadata = { title: "Check a member" };

export default async function MemberCheck() {
  await requireOrg();
  const payers = await api<{ data: Payer[] }>("/v1/payers");
  const list = (payers.ok ? payers.data.data : []).sort((a, b) =>
    a.name.localeCompare(b.name),
  );
  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/claims">Claims</Link>
          </p>
          <h1>Check a member</h1>
          <p className="app-finding">
            Ask the insurer whether a member is covered before the visit.
          </p>
        </div>
      </div>
      <section className="app-card app-measure-card" aria-label="Member check">
        <MemberCheckForm payers={list} />
        <p className="app-muted">
          In the sandbox, member numbers starting NOTFOUND, INACTIVE or OTP show
          the other answers. The sandbox code is 123456.
        </p>
      </section>
    </div>
  );
}
