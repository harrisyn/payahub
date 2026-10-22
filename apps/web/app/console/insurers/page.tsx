import Link from "next/link";

import { togglePayer } from "@/app/actions";
import { api } from "@/lib/api";
import { requireOrg } from "@/lib/session";
import type { ConnectorAccount, Payer, PayerSetting } from "@/lib/types";

export const metadata = { title: "Insurers" };

export default async function Insurers() {
  const { organization: org } = await requireOrg();
  const [payers, settings, accounts] = await Promise.all([
    api<{ data: Payer[] }>("/v1/payers"),
    api<{ data: PayerSetting[] }>("/v1/payer-settings"),
    api<{ data: ConnectorAccount[] }>("/v1/connector-accounts"),
  ]);
  const list = payers.ok ? payers.data.data : [];
  const rows = settings.ok ? settings.data.data : [];
  const accts = accounts.ok ? accounts.data.data : [];
  const setting = (id: string, env: string) => rows.find((r) => r.payer_id === id && r.environment === env);
  const account = (connector: string, env: string) => accts.find((a) => a.connector === connector && a.environment === env);
  const wanted = new Set(org.profile?.payers ?? []);
  const sorted = [...list].sort((a, b) => Number(wanted.has(b.id)) - Number(wanted.has(a.id)));
  const enabled = rows.filter((r) => r.enabled).length;

  // The platforms behind the insurers in use, each with its one account.
  const platforms = new Map<string, { name: string; payers: string[] }>();
  for (const p of list) {
    if (p.kind !== "integrated" || !rows.some((r) => r.payer_id === p.id && r.enabled)) continue;
    const entry = platforms.get(p.connector.id) ?? { name: p.connector.name, payers: [] };
    entry.payers.push(p.name);
    platforms.set(p.connector.id, entry);
  }

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Insurers</h1>
          <p className="app-finding">
            {enabled
              ? `${enabled} insurer setting${enabled === 1 ? "" : "s"} on. An insurer behind a platform needs your account on that platform, once.`
              : "Pick the insurers you bill from our list. Start with the sandbox ones to send a first claim."}
          </p>
        </div>
      </div>
      <section className="app-card app-card--flush" aria-label="Insurers">
        <table className="app-table">
          <thead>
            <tr>
              <th scope="col">Insurer</th>
              <th scope="col" className="hide-sm">
                Reached through
              </th>
              <th scope="col" className="hide-sm">
                Status
              </th>
              <th scope="col">Sandbox</th>
              <th scope="col">Live account</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((p) => {
              const sandbox = setting(p.id, "sandbox");
              const live = setting(p.id, "live");
              const acct = account(p.connector.id, "live");
              return (
                <tr key={p.id}>
                  <td>
                    <Link href={`/console/insurers/${p.id}`} className="app-strong">
                      {p.name}
                    </Link>
                    {wanted.has(p.id) && <span className="app-tag hide-sm">You bill them</span>}
                  </td>
                  <td className="app-muted hide-sm">{p.kind === "integrated" ? p.connector.name : "Directly"}</td>
                  <td className="hide-sm">
                    <span className={`app-pill app-pill--${p.status === "available" ? "good" : "wait"}`}>
                      {p.status === "available" ? "Available" : "Connecting"}
                    </span>
                  </td>
                  <td>
                    <form action={togglePayer}>
                      <input type="hidden" name="payer" value={p.id} />
                      <input type="hidden" name="environment" value="sandbox" />
                      <input type="hidden" name="enabled" value={String(!sandbox?.enabled)} />
                      <button
                        type="submit"
                        role="switch"
                        aria-checked={!!sandbox?.enabled}
                        aria-label={`${p.name} sandbox`}
                        className="app-toggle"
                      />
                    </form>
                  </td>
                  <td>
                    {acct ? (
                      <span className={`app-pill app-pill--${acct.check_ok ? "good" : acct.check_ok === false ? "wait" : "neutral"}`}>
                        {acct.check_ok ? "Checked" : acct.check_ok === false ? "Saved, not checked" : "Saved"}
                      </span>
                    ) : live?.account === "need" ? (
                      <span className="app-pill app-pill--wait">Account needed</span>
                    ) : p.id === "sandbox" ? (
                      <span className="app-muted">None needed</span>
                    ) : (
                      <span className="app-dash">No account</span>
                    )}
                  </td>
                  <td className="app-right">
                    <Link href={`/console/insurers/${p.id}`} className="app-button">
                      Set up
                    </Link>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </section>

      {platforms.size > 0 && (
        <section className="app-card" aria-labelledby="platforms-h">
          <h2 id="platforms-h" className="app-card__title">
            Your platform accounts
          </h2>
          <ul className="app-trail">
            {[...platforms].map(([id, pl]) => {
              const live = account(id, "live");
              const sandbox = account(id, "sandbox");
              return (
                <li key={id} className="app-trail__item">
                  <span className="app-strong">{pl.name}</span>
                  <span>
                    Reaches {pl.payers.join(", ")}.{" "}
                    <span className="app-muted">
                      Live: {live ? (live.check_ok ? "checked" : "saved") : "no account"}. Sandbox:{" "}
                      {sandbox ? (sandbox.check_ok ? "checked" : "saved") : "no account"}.
                    </span>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      )}
      <p className="app-muted app-note">
        Insurers are one shared list: we set each one up once, for everyone. Connecting means we are still porting the
        platform that insurer sits behind; you can save your account now and we check it when it goes live. Missing an
        insurer? Tell us and we add it for every clinic.
      </p>
    </div>
  );
}
