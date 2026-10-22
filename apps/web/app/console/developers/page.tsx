import Link from "next/link";

import { revokeKey } from "@/app/actions";
import { KeyForm, Submit } from "@/components/forms";
import { KeyBanner } from "@/components/key-banner";
import { api, API_URL } from "@/lib/api";
import { date } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { APIKey } from "@/lib/types";

export const metadata = { title: "Developers" };

export default async function Developers() {
  await requireOrg();
  const keys = await api<{ data: APIKey[] }>("/v1/api-keys");
  const rows = keys.ok ? keys.data.data : [];
  const active = rows.filter((k) => !k.revoked_at);
  const base = process.env.NEXT_PUBLIC_API_URL ?? API_URL;

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Developers</h1>
          <p className="app-finding">
            {active.length} active key{active.length === 1 ? "" : "s"}. Sandbox keys reach only the sandbox insurer; live keys send
            real claims.
          </p>
        </div>
        <Link href="/docs" className="app-button">
          API reference
        </Link>
      </div>

      <KeyBanner />

      <section className="app-card" aria-labelledby="keys-h">
        <div className="app-card__head">
          <h2 id="keys-h" className="app-card__title">
            API keys
          </h2>
        </div>
        <KeyForm />
        <table className="app-table">
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Environment</th>
              <th scope="col">Key</th>
              <th scope="col">Created</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((k) => (
              <tr key={k.id} className={k.revoked_at ? "is-revoked" : ""}>
                <td className="app-strong">{k.name || "Unnamed key"}</td>
                <td>
                  <span className={`app-pill app-pill--${k.environment === "live" ? "good" : "neutral"}`}>
                    {k.environment === "live" ? "Live" : "Sandbox"}
                  </span>
                </td>
                <td className="app-id">{k.prefix}…</td>
                <td className="app-muted">{k.revoked_at ? `Revoked ${date(k.revoked_at)}` : date(k.created_at)}</td>
                <td className="app-right">
                  {!k.revoked_at && (
                    <form action={revokeKey}>
                      <input type="hidden" name="id" value={k.id} />
                      <Submit className="app-button app-button--danger">Revoke</Submit>
                    </form>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="app-card" aria-labelledby="qs-h">
        <h2 id="qs-h" className="app-card__title">
          Send a claim from your system
        </h2>
        <pre className="app-pre">
          <code>{`curl ${base}/v1/claims \\
  -H "Authorization: Bearer <your sandbox key>" \\
  -H "Idempotency-Key: <your visit or invoice id>" \\
  -d '{"payer_id":"sandbox","member":{"member_number":"M-1001"},
       "provider":{"name":"Your clinic"},"encounter":{"start_date":"${new Date().toISOString().slice(0, 10)}"},
       "lines":[{"description":"Consultation","unit_price":15000}],
       "diagnoses":[{"code":{"system":"icd-10","value":"B54"},"role":"primary"}]}'

curl -X POST ${base}/v1/claims/<id>/submit -H "Authorization: Bearer <your sandbox key>"`}</code>
        </pre>
        <p className="app-muted">
          Amounts are in pesewas, so 15000 is GHS 150.00. Member numbers starting NOTFOUND, OTP, REJECT, FLAKY or LOSTREPLY
          take the sandbox&apos;s failure paths.
        </p>
      </section>
    </div>
  );
}
