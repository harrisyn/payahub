import Link from "next/link";

import { api } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { requireOperator } from "@/lib/session";

export const metadata = { title: "Enquiries" };

type Lead = {
  id: string;
  kind: "contact" | "platform" | "payer" | "specs";
  name: string;
  email: string;
  phone: string;
  organization: string;
  payer_name: string;
  monthly_claims: number | null;
  payer_contact: string;
  message: string;
  created_at: string;
};

const kinds: { kind: Lead["kind"] | ""; label: string }[] = [
  { kind: "", label: "All" },
  { kind: "contact", label: "Contact me" },
  { kind: "platform", label: "Platform not listed" },
  { kind: "payer", label: "Payer requests" },
  { kind: "specs", label: "Interface specs" },
];

const kindLabel = Object.fromEntries(kinds.map((k) => [k.kind, k.label])) as Record<string, string>;

// What people asked for on the public site. Payer requests are summed by
// payer, so the most-wanted payers with the most claims come first.
export default async function Enquiries({ searchParams }: { searchParams: Promise<{ kind?: string }> }) {
  await requireOperator();
  const { kind = "" } = await searchParams;
  const shown = kinds.some((k) => k.kind === kind) ? kind : "";
  const res = await api<{ data: Lead[] }>(`/v1/operator/leads${shown ? `?kind=${shown}` : ""}`);
  const leads = res.ok ? res.data.data : [];

  const wanted = new Map<string, { name: string; asks: number; claims: number }>();
  for (const l of leads.filter((l) => l.kind === "payer")) {
    const k = l.payer_name.toLowerCase();
    const w = wanted.get(k) ?? { name: l.payer_name, asks: 0, claims: 0 };
    w.asks++;
    w.claims += l.monthly_claims ?? 0;
    wanted.set(k, w);
  }
  const top = [...wanted.values()].sort((a, b) => b.claims - a.claims || b.asks - a.asks).slice(0, 5);

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Enquiries</h1>
          <p className="app-finding">
            {leads.length} {leads.length === 1 ? "enquiry" : "enquiries"} from the public site
            {shown ? `: ${kindLabel[shown].toLowerCase()}` : ""}.
          </p>
        </div>
      </div>

      <nav aria-label="Kind" className="app-tabs">
        {kinds.map((k) => (
          <Link
            key={k.kind}
            href={k.kind ? `/console/operator/leads?kind=${k.kind}` : "/console/operator/leads"}
            className="app-tab"
            aria-current={shown === k.kind ? "page" : undefined}
          >
            {k.label}
          </Link>
        ))}
      </nav>

      {!res.ok && <p className="form-error">{res.error.message}</p>}

      {top.length > 0 && (
        <section className="app-card" aria-labelledby="wanted-h">
          <h2 id="wanted-h">Most-wanted payers</h2>
          <ul className="app-list">
            {top.map((w) => (
              <li key={w.name}>
                <strong>{w.name}</strong>{" "}
                <span className="app-muted">
                  {w.asks} {w.asks === 1 ? "request" : "requests"}, about {w.claims.toLocaleString("en-GH")} claims a
                  month
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="app-card app-card--flush" aria-label="Enquiries">
        {leads.length === 0 ? (
          <p className="app-empty">Nothing yet. Enquiries from the landing page arrive here.</p>
        ) : (
          <table className="app-table">
            <thead>
              <tr>
                <th scope="col">Who</th>
                <th scope="col">Asked for</th>
                <th scope="col" className="hide-sm">
                  Details
                </th>
                <th scope="col">Received</th>
              </tr>
            </thead>
            <tbody>
              {leads.map((l) => (
                <tr key={l.id}>
                  <td>
                    <strong>{l.name || l.organization || l.email}</strong>
                    <span className="app-muted app-block">
                      <a href={`mailto:${l.email}`}>{l.email}</a>
                      {l.phone && (
                        <>
                          {" · "}
                          <a href={`tel:${l.phone}`}>{l.phone}</a>
                        </>
                      )}
                    </span>
                    {l.organization && l.name && <span className="app-muted app-block">{l.organization}</span>}
                  </td>
                  <td>{kindLabel[l.kind]}</td>
                  <td className="hide-sm">
                    {l.kind === "payer" && (
                      <>
                        <strong>{l.payer_name}</strong>
                        {l.monthly_claims != null && (
                          <span className="app-block">{l.monthly_claims.toLocaleString("en-GH")} claims a month</span>
                        )}
                        {l.payer_contact && <span className="app-muted app-block">Contact: {l.payer_contact}</span>}
                      </>
                    )}
                    {l.message && <span className="app-block">{l.message}</span>}
                  </td>
                  <td>{dateTime(l.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
