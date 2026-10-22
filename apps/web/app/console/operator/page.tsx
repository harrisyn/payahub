import Link from "next/link";

import { api } from "@/lib/api";
import { fieldLabels } from "@/lib/format";
import { requireOperator } from "@/lib/session";
import type { Connector, OpPayer } from "@/lib/types";

export const metadata = { title: "Catalogue" };

// The payers every organisation picks from. Operators add them, say how each
// is reached, and map its codes.
export default async function Catalogue() {
  await requireOperator();
  const [res, connectors] = await Promise.all([
    api<{ data: OpPayer[] }>("/v1/operator/payers"),
    api<{ data: Connector[] }>("/v1/connectors"),
  ]);
  const label = fieldLabels(connectors.ok ? connectors.data.data : []);
  const payers = res.ok ? res.data.data : [];
  const live = payers.filter((p) => !p.inactive);
  const ready = live.filter((p) => p.status === "available").length;
  const unmapped = live.filter((p) => p.coded_items < p.total_items).length;

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Catalogue</h1>
          <p className="app-finding">
            {ready} of {live.length} payers take claims today. {unmapped ? `${unmapped} still have standard items without a code.` : "Every payer has a code for every standard item."}
          </p>
        </div>
        <Link href="/console/operator/new" className="app-button app-button--primary">
          Add a payer
        </Link>
      </div>

      {!res.ok && <p className="form-error">{res.error.message}</p>}

      <section className="app-card app-card--flush" aria-label="Payers">
        <table className="app-table">
          <thead>
            <tr>
              <th scope="col">Payer</th>
              <th scope="col" className="hide-sm">
                Reached through
              </th>
              <th scope="col">Status</th>
              <th scope="col">Codes</th>
              <th scope="col" className="hide-sm">
                Payer&apos;s list
              </th>
            </tr>
          </thead>
          <tbody>
            {payers.map((p) => {
              const share = p.total_items ? p.coded_items / p.total_items : 0;
              return (
                <tr key={p.id}>
                  <td>
                    <Link href={`/console/operator/${p.id}`} className="app-strong-link">
                      {p.name}
                    </Link>
                    <span className="app-muted app-block">
                      {p.id} · {p.country}
                      <span className="show-sm"> via {p.connector.name}</span>
                    </span>
                  </td>
                  <td className="hide-sm">
                    {p.connector.name}
                    <span className="app-muted app-block">{p.kind === "integrated" ? "Platform" : "Direct"}</span>
                  </td>
                  <td>
                    {p.inactive ? (
                      <span className="app-pill app-pill--neutral">Withdrawn</span>
                    ) : p.missing_fields.length ? (
                      <span className="app-pill app-pill--bad">Needs {p.missing_fields.map((f) => label(p.connector.id, f)).join(", ")}</span>
                    ) : p.status === "available" ? (
                      <span className="app-pill app-pill--good">Taking claims</span>
                    ) : (
                      <span className="app-pill app-pill--wait">Connector not ported</span>
                    )}
                  </td>
                  <td className="app-tnum">
                    {p.coded_items} of {p.total_items}
                    <span className={`app-meter${share < 1 ? " app-meter--part" : ""}`} aria-hidden="true">
                      <span style={{ width: `${Math.round(share * 100)}%` }} />
                    </span>
                  </td>
                  <td className="hide-sm app-tnum">
                    {p.list_size ? `${p.list_size.toLocaleString("en-GB")} codes` : <span className="app-dash">–</span>}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </section>
    </div>
  );
}
