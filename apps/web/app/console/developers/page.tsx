import Link from "next/link";

import { pingWebhook, removeWebhook, revokeKey } from "@/app/actions";
import { KeyForm, Submit } from "@/components/forms";
import { WebhookForm } from "@/components/webhook-form";
import { KeyBanner } from "@/components/key-banner";
import { api, API_URL } from "@/lib/api";
import { date, dateTime } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { APIKey, WebhookEndpoint } from "@/lib/types";

export const metadata = { title: "Developers" };

export default async function Developers() {
  await requireOrg();
  const [keys, sandboxHooks, liveHooks] = await Promise.all([
    api<{ data: APIKey[] }>("/v1/api-keys"),
    api<{ data: WebhookEndpoint[] }>("/v1/webhook-endpoints?environment=sandbox"),
    api<{ data: WebhookEndpoint[] }>("/v1/webhook-endpoints?environment=live"),
  ]);
  const hooks = [...(liveHooks.ok ? liveHooks.data.data : []), ...(sandboxHooks.ok ? sandboxHooks.data.data : [])];
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

      <section className="app-card" aria-labelledby="hooks-h">
        <div className="app-card__head">
          <h2 id="hooks-h" className="app-card__title">
            Webhooks
          </h2>
        </div>
        <p className="app-muted app-measure">
          Your system hears about every claim as it moves: received, checked, sent, accepted or rejected by the insurer. Each event is
          signed with the endpoint&rsquo;s secret and retried for about two and a half days if your system does not answer.
        </p>
        <WebhookForm />
        {hooks.length > 0 && (
          <table className="app-table app-table--stack">
            <thead>
              <tr>
                <th scope="col">Endpoint</th>
                <th scope="col">Environment</th>
                <th scope="col">Deliveries</th>
                <th scope="col">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {hooks.map((h) => (
                <tr key={h.id}>
                  <td>
                    <Link href={`/console/developers/webhooks/${h.id}`} className="app-strong">
                      {h.description || "Endpoint"}
                    </Link>
                    <span className="app-muted app-block app-id app-wrap">{h.url}</span>
                  </td>
                  <td>
                    <span className={`app-pill app-pill--${h.environment === "live" ? "good" : "neutral"}`}>
                      {h.environment === "live" ? "Live" : "Sandbox"}
                    </span>
                  </td>
                  <td>
                    {h.failing > 0 ? (
                      <span className="app-bad">
                        {h.failing} retrying: {h.last_error}
                      </span>
                    ) : (
                      <span>{h.delivered_24h} delivered today</span>
                    )}
                    <span className="app-muted app-block">
                      {h.last_delivered_at ? `Last delivered ${dateTime(h.last_delivered_at)}` : "Nothing delivered yet"}
                    </span>
                  </td>
                  <td className="app-right">
                    <div className="app-actions">
                      <form action={pingWebhook}>
                        <input type="hidden" name="id" value={h.id} />
                        <Submit className="app-button">Send a test</Submit>
                      </form>
                      <form action={removeWebhook}>
                        <input type="hidden" name="id" value={h.id} />
                        <Submit className="app-button app-button--danger">Remove</Submit>
                      </form>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
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

# Or create, send and wait for the insurer's answer in one call:
curl "${base}/v1/claims?submit=true" -H "Prefer: wait=20" ...

curl -X POST ${base}/v1/claims/<id>/submit -H "Authorization: Bearer <your sandbox key>"`}</code>
        </pre>
        <p className="app-muted">
          Amounts are in pesewas, so 15000 is GHS 150.00. Member numbers starting NOTFOUND, OTP, REJECT, FLAKY, LOSTREPLY or SLOW
          take the sandbox&apos;s failure paths.
        </p>
      </section>
    </div>
  );
}
