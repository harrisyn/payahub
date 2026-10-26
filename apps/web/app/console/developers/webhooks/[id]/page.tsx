import Link from "next/link";
import { notFound } from "next/navigation";

import { api } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { WebhookDelivery } from "@/lib/types";

export const metadata = { title: "Webhook deliveries" };

const statusText = { delivered: "Delivered", pending: "Retrying", failed: "Gave up" } as const;
const statusTone = { delivered: "good", pending: "neutral", failed: "bad" } as const;

export default async function Deliveries({ params }: { params: Promise<{ id: string }> }) {
  await requireOrg();
  const { id } = await params;
  const res = await api<{ data: WebhookDelivery[] }>(`/v1/webhook-endpoints/${encodeURIComponent(id)}/deliveries`);
  if (!res.ok) notFound();
  const rows = res.data.data;
  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/developers">Developers</Link>
          </p>
          <h1>Webhook deliveries</h1>
          <p className="app-finding">The last 50 events sent to this endpoint, newest first.</p>
        </div>
      </div>
      <section className="app-card">
        {rows.length === 0 ? (
          <p className="app-empty">Nothing yet. Events appear here as your claims move, or when you send a test.</p>
        ) : (
          <table className="app-table app-table--stack">
            <thead>
              <tr>
                <th scope="col">Event</th>
                <th scope="col">Claim</th>
                <th scope="col">Result</th>
                <th scope="col">When</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((d) => (
                <tr key={d.id}>
                  <td className="app-id">{d.event_type}</td>
                  <td>
                    {d.payload.data?.claim_id ? (
                      <Link href={`/console/claims/${d.payload.data.claim_id}`} className="app-id">
                        …{d.payload.data.claim_id.slice(-8)}
                      </Link>
                    ) : (
                      <span className="app-muted">Test</span>
                    )}
                  </td>
                  <td>
                    <span className={`app-pill app-pill--${statusTone[d.status]}`}>{statusText[d.status]}</span>
                    <span className="app-muted app-block">
                      {d.status === "delivered"
                        ? `HTTP ${d.last_status}${d.attempts > 1 ? ` after ${d.attempts} attempts` : ""}`
                        : `${d.attempts} attempt${d.attempts === 1 ? "" : "s"}${d.last_error ? `: ${d.last_error}` : ""}`}
                    </span>
                  </td>
                  <td className="app-muted">{dateTime(d.delivered_at ?? d.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
