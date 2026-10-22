import Link from "next/link";

import { PayerForm } from "@/components/operator-forms";
import { api } from "@/lib/api";
import { requireOperator } from "@/lib/session";
import type { Connector } from "@/lib/types";

export const metadata = { title: "Add a payer" };

export default async function NewPayer() {
  await requireOperator();
  const connectors = await api<{ data: Connector[] }>("/v1/connectors");
  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/operator">Catalogue</Link>
          </p>
          <h1>Add a payer</h1>
          <p className="app-finding">
            Added once, offered to every organisation. If the payer sits behind a platform, organisations use their own
            account on it.
          </p>
        </div>
      </div>
      <section className="app-card app-narrow">
        <PayerForm connectors={connectors.ok ? connectors.data.data : []} />
      </section>
    </div>
  );
}
