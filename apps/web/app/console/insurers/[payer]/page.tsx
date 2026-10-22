import Link from "next/link";
import { notFound } from "next/navigation";

import { CheckForm, PayerForm } from "@/components/forms";
import { api } from "@/lib/api";
import { dateTime } from "@/lib/format";
import type { Manifest, PayerSetting } from "@/lib/types";

export const metadata = { title: "Insurer" };

const verbName: Record<string, string> = {
  verify_member: "Check membership",
  send_otp: "Send the member a code",
  request_authorization: "Ask for authorisation",
  submit_whole_claim: "Send a whole claim",
  open_claim: "Open a claim at the visit",
  add_line: "Add lines as they happen",
  update_line: "Change a line",
  delete_line: "Remove a line",
  add_diagnosis: "Add diagnoses",
  finalize_claim: "Submit the claim",
  withdraw_claim: "Withdraw a claim",
  find_claim: "Look a claim up",
  list_catalog: "Fetch the insurer's code list",
};

export default async function Insurer({ params, searchParams }: { params: Promise<{ payer: string }>; searchParams: Promise<{ env?: string }> }) {
  const { payer } = await params;
  const env = (await searchParams).env === "sandbox" ? "sandbox" : "live";
  const [payers, settings] = await Promise.all([
    api<{ data: Manifest[] }>("/v1/payers"),
    api<{ data: PayerSetting[] }>("/v1/payer-settings"),
  ]);
  const m = payers.ok ? payers.data.data.find((p) => p.id === payer) : undefined;
  if (!m) notFound();
  const setting = settings.ok ? settings.data.data.find((s) => s.payer_id === payer && s.environment === env) : undefined;

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/insurers">Insurers</Link>
          </p>
          <h1>{m.name}</h1>
          <p className="app-finding">
            {m.status === "available"
              ? "Available now."
              : "Connecting: credentials can be saved now and are checked when this insurer goes live."}
          </p>
        </div>
      </div>

      <nav className="app-tabs" aria-label="Environment">
        <Link href={`?env=live`} aria-current={env === "live" ? "page" : undefined}>
          Live
        </Link>
        <Link href={`?env=sandbox`} aria-current={env === "sandbox" ? "page" : undefined}>
          Sandbox
        </Link>
      </nav>

      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="creds-h">
          <h2 id="creds-h" className="app-card__title">
            {env === "live" ? "Live credentials" : "Sandbox credentials"}
          </h2>
          <PayerForm key={env} manifest={m} env={env} setting={setting} />
        </section>

        <div className="app-stack">
          <section className="app-card" aria-labelledby="check-h">
            <h2 id="check-h" className="app-card__title">
              Connection
            </h2>
            {setting?.checked_at ? (
              <p className={setting.check_ok ? "app-good" : "app-warn"}>
                {setting.check_message}
                <span className="app-muted app-block">Checked {dateTime(setting.checked_at)}</span>
              </p>
            ) : (
              <p className="app-muted">Not checked yet.</p>
            )}
            {(setting || m.id === "sandbox") && <CheckForm payer={m.id} env={env} />}
          </section>
          <section className="app-card" aria-labelledby="caps-h">
            <h2 id="caps-h" className="app-card__title">
              What {m.name} supports
            </h2>
            <ul className="app-list">
              {m.capabilities.map((c) => (
                <li key={c}>{verbName[c] ?? c}</li>
              ))}
            </ul>
            {m.member_otp_required && (
              <p className="app-muted">At the desk, the member reads you a code the insurer sends to their phone.</p>
            )}
          </section>
        </div>
      </div>
    </div>
  );
}
