import Link from "next/link";

import { dateTime, money, stateLabel, stateTone } from "@/lib/format";
import type { ClaimSummary } from "@/lib/types";

export function ClaimsTable({ rows, payerNames }: { rows: ClaimSummary[]; payerNames: Record<string, string> }) {
  return (
    <table className="app-table">
      <thead>
        <tr>
          <th scope="col">Claim</th>
          <th scope="col" className="hide-sm">Member</th>
          <th scope="col" className="hide-sm">
            Insurer
          </th>
          <th scope="col" className="num">
            Amount
          </th>
          <th scope="col">State</th>
          <th scope="col" className="hide-sm">
            Insurer&apos;s number
          </th>
          <th scope="col" className="hide-sm">
            Updated
          </th>
        </tr>
      </thead>
      <tbody>
        {rows.map((c) => (
          <tr key={c.id}>
            <td>
              <Link href={`/console/claims/${c.id}`} className="app-id">
                {c.id.slice(0, 16)}…
              </Link>
              {c.environment === "sandbox" && <span className="app-tag hide-sm">Sandbox</span>}
              <span className="show-sm app-muted">{c.member_number}</span>
            </td>
            <td className="hide-sm">{c.member_number}</td>
            <td className="hide-sm">{payerNames[c.payer_id] ?? c.payer_id}</td>
            <td className="num">{money(c.total, c.currency)}</td>
            <td>
              <span className={`app-pill app-pill--${stateTone(c.state)}`}>{stateLabel[c.state]}</span>
            </td>
            <td className="app-id hide-sm">{c.payer_claim_number ?? <span className="app-dash">–</span>}</td>
            <td className="app-muted hide-sm">{dateTime(c.updated_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
