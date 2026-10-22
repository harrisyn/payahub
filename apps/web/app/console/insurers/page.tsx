import Link from "next/link";

import { togglePayer } from "@/app/actions";
import { api } from "@/lib/api";
import { requireOrg } from "@/lib/session";
import type { Manifest, PayerSetting } from "@/lib/types";

export const metadata = { title: "Insurers" };

export default async function Insurers() {
  const { organization: org } = await requireOrg();
  const [payers, settings] = await Promise.all([
    api<{ data: Manifest[] }>("/v1/payers"),
    api<{ data: PayerSetting[] }>("/v1/payer-settings"),
  ]);
  const list = payers.ok ? payers.data.data : [];
  const rows = settings.ok ? settings.data.data : [];
  const setting = (id: string, env: string) => rows.find((r) => r.payer_id === id && r.environment === env);
  const wanted = new Set(org.profile?.payers ?? []);
  const sorted = [...list].sort((a, b) => Number(wanted.has(b.id)) - Number(wanted.has(a.id)));
  const enabled = rows.filter((r) => r.enabled).length;

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Insurers</h1>
          <p className="app-finding">
            {enabled
              ? `${enabled} connection${enabled === 1 ? "" : "s"} on. Live claims need each insurer's credentials, checked.`
              : "Turn on the sandbox insurer to send a first claim, then add your real insurers' credentials."}
          </p>
        </div>
      </div>
      <section className="app-card app-card--flush" aria-label="Insurers">
        <table className="app-table">
          <thead>
            <tr>
              <th scope="col">Insurer</th>
              <th scope="col">Status</th>
              <th scope="col">Member code at the desk</th>
              <th scope="col">Sandbox</th>
              <th scope="col">Live</th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((m) => {
              const live = setting(m.id, "live");
              const sandbox = setting(m.id, "sandbox");
              return (
                <tr key={m.id}>
                  <td>
                    <Link href={`/console/insurers/${m.id}`} className="app-strong">
                      {m.name}
                    </Link>
                    {wanted.has(m.id) && <span className="app-tag">You bill them</span>}
                  </td>
                  <td>
                    <span className={`app-pill app-pill--${m.status === "available" ? "good" : "wait"}`}>
                      {m.status === "available" ? "Available" : "Connecting"}
                    </span>
                  </td>
                  <td className="app-muted">{m.member_otp_required ? "SMS code from the member" : "None"}</td>
                  <td>
                    <form action={togglePayer}>
                      <input type="hidden" name="payer" value={m.id} />
                      <input type="hidden" name="environment" value="sandbox" />
                      <input type="hidden" name="enabled" value={String(!sandbox?.enabled)} />
                      <button
                        type="submit"
                        role="switch"
                        aria-checked={!!sandbox?.enabled}
                        aria-label={`${m.name} sandbox`}
                        className="app-toggle"
                      />
                    </form>
                  </td>
                  <td>
                    {live?.credential_fields.length ? (
                      <span className={`app-pill app-pill--${live.check_ok ? "good" : live.check_ok === false ? "wait" : "neutral"}`}>
                        {live.check_ok ? "Checked" : live.check_ok === false ? "Saved, not checked" : "Saved"}
                      </span>
                    ) : m.credentials.length ? (
                      <span className="app-dash">No credentials</span>
                    ) : (
                      <span className="app-muted">None needed</span>
                    )}
                  </td>
                  <td className="app-right">
                    <Link href={`/console/insurers/${m.id}`} className="app-button">
                      {m.credentials.length ? "Credentials" : "Settings"}
                    </Link>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </section>
      <p className="app-muted app-note">
        Connecting means we are porting that insurer&apos;s integration from Serenity. You can save credentials now; we
        check them as soon as it goes live.
      </p>
    </div>
  );
}
