import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountForm, CheckForm, PayerUseForm } from "@/components/forms";
import { api } from "@/lib/api";
import { dateTime } from "@/lib/format";
import type { Connector, ConnectorAccount, Payer, PayerSetting } from "@/lib/types";

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

export default async function Insurer({
  params,
  searchParams,
}: {
  params: Promise<{ payer: string }>;
  searchParams: Promise<{ env?: string }>;
}) {
  const { payer: id } = await params;
  const env = (await searchParams).env === "sandbox" ? "sandbox" : "live";
  const [payers, connectors, settings, accounts] = await Promise.all([
    api<{ data: Payer[] }>("/v1/payers"),
    api<{ data: Connector[] }>("/v1/connectors"),
    api<{ data: PayerSetting[] }>("/v1/payer-settings"),
    api<{ data: ConnectorAccount[] }>("/v1/connector-accounts"),
  ]);
  const all = payers.ok ? payers.data.data : [];
  const p = all.find((x) => x.id === id);
  const c = connectors.ok ? connectors.data.data.find((x) => x.id === p?.connector.id) : undefined;
  if (!p || !c) notFound();
  const setting = settings.ok ? settings.data.data.find((s) => s.payer_id === id && s.environment === env) : undefined;
  const account = accounts.ok ? accounts.data.data.find((a) => a.connector === c.id && a.environment === env) : undefined;
  const neighbours = all.filter((x) => x.connector.id === c.id && x.id !== p.id).map((x) => x.name);
  const integrated = p.kind === "integrated";
  const needsAccount = c.credentials.length > 0;
  const wantsForm = needsAccount && (!integrated || setting?.account !== "need");

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/insurers">Insurers</Link>
          </p>
          <h1>{p.name}</h1>
          <p className="app-finding">
            {integrated ? `Reached through ${c.name}. ` : "Reached directly. "}
            {p.status === "available"
              ? "Available now."
              : `Connecting: your account can be saved now and is checked when ${c.name} goes live.`}
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
        <div className="app-stack">
          <section className="app-card" aria-labelledby="use-h">
            <h2 id="use-h" className="app-card__title">
              Use {p.name}
            </h2>
            <PayerUseForm key={`use-${env}`} payer={p} env={env} setting={setting} />
          </section>

          <section className="app-card" aria-labelledby="acct-h">
            <h2 id="acct-h" className="app-card__title">
              Your {c.name} account{env === "sandbox" ? " (sandbox)" : ""}
            </h2>
            {!needsAccount ? (
              <p className="app-muted">{c.name} needs no account. Claims go straight through.</p>
            ) : wantsForm ? (
              <>
                {integrated && (
                  <p className="app-muted">
                    Enter it once. It is used for every insurer {c.name} carries
                    {neighbours.length ? `, including ${neighbours.join(", ")}` : ""}.
                  </p>
                )}
                <AccountForm key={`acct-${env}`} connector={c} env={env} account={account} />
              </>
            ) : (
              <p>
                Ask {c.name} for a provider account for your facility, then come back and choose Yes above. Until then,
                you can send {p.name} claims in the sandbox.
              </p>
            )}
          </section>
        </div>

        <div className="app-stack">
          <section className="app-card" aria-labelledby="check-h">
            <h2 id="check-h" className="app-card__title">
              Connection
            </h2>
            {account?.checked_at ? (
              <p className={account.check_ok ? "app-good" : "app-warn"}>
                {account.check_message}
                <span className="app-muted app-block">Checked {dateTime(account.checked_at)}</span>
              </p>
            ) : (
              <p className="app-muted">Not checked yet.</p>
            )}
            {(account || !needsAccount) && <CheckForm connector={c.id} env={env} show={!needsAccount} />}
          </section>
          <section className="app-card" aria-labelledby="caps-h">
            <h2 id="caps-h" className="app-card__title">
              What {p.name} supports
            </h2>
            <ul className="app-list">
              {p.capabilities.map((v) => (
                <li key={v}>{verbName[v] ?? v}</li>
              ))}
            </ul>
            <p className="app-muted">
              {p.coded_items
                ? `${p.coded_items} standard items have ${p.name}'s codes, so mapped lines need no retyping.`
                : `${p.name}'s codes are not loaded yet; lines need their code until they are.`}
              {p.member_otp_required && " At the desk, the member reads you a code the insurer sends to their phone."}
            </p>
          </section>
        </div>
      </div>
    </div>
  );
}
