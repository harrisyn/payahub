import { DecideWithdrawal } from "@/components/wallet-forms";
import { api } from "@/lib/api";
import { dateTime, money } from "@/lib/format";
import { requireOperator } from "@/lib/session";
import type { LedgerView, Withdrawal } from "@/lib/types";

export const metadata = { title: "Withdrawals" };

const kindName: Record<string, string> = {
  revenue: "Platform wallet (revenue)",
  cash: "Held at the provider",
  fees: "Provider fees",
  payouts_pending: "Withdrawals being paid",
  adjustments: "Operator adjustments",
};

// Asset and expense accounts are debit-normal: show them as positive amounts.
const shown = (kind: string, balance: number) =>
  kind === "cash" || kind === "fees" || kind === "adjustments"
    ? -balance
    : balance;

export default async function Withdrawals() {
  await requireOperator();
  const [open, done, ledger] = await Promise.all([
    api<{ data: Withdrawal[] }>("/v1/operator/withdrawals?status=requested"),
    api<{ data: Withdrawal[] }>(
      "/v1/operator/withdrawals?status=approved,paying,paid,failed,rejected",
    ),
    api<LedgerView>("/v1/operator/ledger"),
  ]);
  const waiting = open.ok ? open.data.data : [];
  const recent = done.ok ? done.data.data.slice(0, 20) : [];
  const l = ledger.ok ? ledger.data : null;
  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Withdrawals</h1>
          <p className="app-finding">
            {waiting.length
              ? `${waiting.length} waiting for a decision, ${money(waiting.reduce((n, w) => n + w.amount, 0))} in all. The amount is held in each wallet until you decide.`
              : "Nothing is waiting for a decision."}
          </p>
        </div>
      </div>
      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="w-h">
          <h2 id="w-h" className="app-card__title">
            Waiting for approval
          </h2>
          {waiting.length ? (
            <ul className="app-rows app-rows--loose">
              {waiting.map((w) => (
                <li key={w.id}>
                  <div>
                    <span className="app-strong-link">
                      {money(w.amount, w.currency)} to {w.organization_name}
                    </span>
                    <span className="app-muted app-block">
                      {w.destination} · {w.provider} · asked{" "}
                      {dateTime(w.created_at)}
                    </span>
                    {w.reason && (
                      <span className="app-block">“{w.reason}”</span>
                    )}
                  </div>
                  <DecideWithdrawal id={w.id} />
                </li>
              ))}
            </ul>
          ) : (
            <p className="app-empty">
              When an organisation asks to withdraw, it shows here.
            </p>
          )}
          {recent.length > 0 && (
            <>
              <h3 className="app-subtitle">Recent</h3>
              <table className="app-table">
                <tbody>
                  {recent.map((w) => (
                    <tr key={w.id}>
                      <td>
                        {w.organization_name}
                        <span className="app-muted app-block">
                          {w.destination}
                        </span>
                      </td>
                      <td className="app-right app-nowrap">
                        {money(w.amount, w.currency)}
                      </td>
                      <td>
                        <span
                          className={`app-pill ${w.status === "paid" ? "app-pill--good" : w.status === "failed" || w.status === "rejected" ? "app-pill--bad" : "app-pill--wait"}`}
                        >
                          {w.status}
                        </span>
                        {w.message && (
                          <span className="app-muted app-block">
                            {w.message}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </section>
        <section className="app-card" aria-labelledby="l-h">
          <div className="app-card__head">
            <h2 id="l-h" className="app-card__title">
              Ledger
            </h2>
            {l && (
              <span
                className={`app-pill ${l.balanced ? "app-pill--good" : "app-pill--bad"}`}
              >
                {l.balanced ? "Balances" : "Out of balance"}
              </span>
            )}
          </div>
          {l ? (
            <table className="app-table">
              <tbody>
                {Object.entries(l.wallets).map(([cur, n]) => (
                  <tr key={cur}>
                    <td>Owed to organisations (their wallets)</td>
                    <td className="app-right app-nowrap">{money(n, cur)}</td>
                  </tr>
                ))}
                {l.accounts.map((a) => (
                  <tr key={a.id}>
                    <td>
                      {kindName[a.kind] ?? a.kind}
                      {a.id.split(":").length > 3 && (
                        <span className="app-muted app-block">
                          {a.id.split(":")[2]}
                        </span>
                      )}
                    </td>
                    <td className="app-right app-nowrap">
                      {money(shown(a.kind, a.balance), a.currency)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className="app-muted">The ledger could not be read.</p>
          )}
        </section>
      </div>
    </div>
  );
}
