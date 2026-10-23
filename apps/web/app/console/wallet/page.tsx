import Link from "next/link";

import { removePayoutAccount } from "@/app/actions";
import { Submit } from "@/components/forms";
import {
  AddFundsForm,
  PayoutAccountForm,
  WithdrawForm,
} from "@/components/wallet-forms";
import { api } from "@/lib/api";
import { dateTime, money, planName } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type {
  Bank,
  Funding,
  PaymentProvider,
  PayoutAccount,
  Wallet,
  WalletEntry,
  Withdrawal,
} from "@/lib/types";

export const metadata = { title: "Wallet" };

const entryLabel: Record<WalletEntry["kind"], string> = {
  funding: "Added",
  charge: "Claim",
  fee: "Monthly fee",
  withdrawal: "Withdrawal",
  withdrawal_returned: "Returned",
  adjustment: "Adjustment",
};

const withdrawalTone: Record<Withdrawal["status"], [string, string]> = {
  requested: ["Waiting for approval", "app-pill--wait"],
  approved: ["Approved", "app-pill--wait"],
  paying: ["Being paid", "app-pill--wait"],
  paid: ["Paid", "app-pill--good"],
  failed: ["Failed, returned", "app-pill--bad"],
  rejected: ["Rejected, returned", "app-pill--bad"],
};

// What the plan costs, said plainly, for the page's opening line.
function terms(w: Wallet): string {
  const p = w.price;
  if (!p)
    return w.plan === "trial"
      ? "Your trial is free; the wallet pays for a plan once you choose one."
      : "Your plan is invoiced, not paid from the wallet.";
  const parts = [];
  if (p.monthly_fee) parts.push(`${money(p.monthly_fee)} a month`);
  if (p.per_claim)
    parts.push(`${money(p.per_claim)} for each claim an insurer acknowledges`);
  return `${planName[w.plan]} plan: ${parts.join(" plus ")}.`;
}

export default async function WalletPage({
  searchParams,
}: {
  searchParams: Promise<{ reference?: string; before?: string }>;
}) {
  await requireOrg();
  const { reference, before } = await searchParams;
  // A checkout returns here with its reference; reading it settles it.
  const returned = reference
    ? await api<Funding>(`/v1/wallet/fundings/${encodeURIComponent(reference)}`)
    : null;
  const [wr, sr, pr, ar, xr] = await Promise.all([
    api<Wallet>("/v1/wallet"),
    api<{ data: WalletEntry[]; next_before?: string }>(
      `/v1/wallet/transactions?limit=25${before && /^\d+$/.test(before) ? `&before=${before}` : ""}`,
    ),
    api<{ data: PaymentProvider[] }>("/v1/payment-providers"),
    api<{ data: PayoutAccount[] }>("/v1/wallet/payout-accounts"),
    api<{ data: Withdrawal[] }>("/v1/wallet/withdrawals"),
  ]);
  if (!wr.ok) throw new Error(wr.error.message);
  const w = wr.data;
  const entries = sr.ok ? sr.data.data : [];
  const older = sr.ok ? sr.data.next_before : undefined;
  const providers = pr.ok ? pr.data.data : [];
  const payers = providers.filter((p) => p.payouts);
  const accounts = ar.ok ? ar.data.data : [];
  const withdrawals = xr.ok ? xr.data.data : [];
  const banks: Record<string, Bank[]> = Object.fromEntries(
    await Promise.all(
      payers.map(async (p) => {
        const r = await api<{ data: Bank[] }>(
          `/v1/payment-providers/${p.id}/banks?currency=${w.currency}`,
        );
        return [p.id, r.ok ? r.data.data : []] as const;
      }),
    ),
  );
  const owing = w.balance < 0 ? -w.balance : 0;
  const suggested = owing ? Math.ceil(owing / 100) : undefined;
  const f = returned?.ok ? returned.data : null;

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Wallet</h1>
          <p className="app-finding">{terms(w)}</p>
        </div>
      </div>

      {f && f.status === "succeeded" && (
        <div className="app-banner app-banner--good" role="status">
          {money(f.paid_amount ?? f.amount, f.currency)} added to your wallet.
        </div>
      )}
      {f && f.status === "pending" && (
        <div className="app-banner" role="status">
          Your payment of {money(f.amount, f.currency)} is not confirmed yet. It
          is added as soon as the provider confirms it; reload this page in a
          minute.
        </div>
      )}
      {f && (f.status === "failed" || f.status === "abandoned") && (
        <div className="app-banner app-banner--bad" role="alert">
          The payment of {money(f.amount, f.currency)} did not go through
          {f.message ? `: ${f.message}` : "."} Nothing was taken from your
          wallet.
        </div>
      )}
      {w.price && !w.can_send_live && (
        <div className="app-banner app-banner--bad" role="alert">
          {owing
            ? `Your wallet is ${money(owing)} short, so live claims are paused. Add funds to send them again.`
            : "Your wallet does not cover another live claim, so live claims are paused. Add funds to send them again."}
        </div>
      )}

      <section className="app-card" aria-labelledby="bal-h">
        <h2 id="bal-h" className="sr-only">
          Balance
        </h2>
        <dl className="app-strip app-strip--4">
          <div>
            <dt>Available</dt>
            <dd className="app-nowrap">{money(w.available, w.currency)}</dd>
          </div>
          <div>
            <dt>Held for claims in flight</dt>
            <dd className="app-nowrap">{money(w.reserved, w.currency)}</dd>
          </div>
          <div>
            <dt>Withdrawals being paid</dt>
            <dd className="app-nowrap">
              {money(w.pending_withdrawals, w.currency)}
            </dd>
          </div>
          <div>
            <dt>Charged this month</dt>
            <dd className="app-nowrap">
              {money(w.charged_this_month, w.currency)}
            </dd>
          </div>
        </dl>
      </section>

      <div className="app-split app-split--wide app-wallet">
        <section className="app-card" aria-labelledby="stmt-h">
          <div className="app-card__head">
            <h2 id="stmt-h" className="app-card__title">
              Statement
            </h2>
            <span className="app-muted">
              Balance {money(w.balance, w.currency)}
            </span>
          </div>
          {entries.length ? (
            <>
              <table className="app-table app-stmt">
                <thead>
                  <tr>
                    <th scope="col">When</th>
                    <th scope="col">What</th>
                    <th scope="col" className="app-right">
                      Amount
                    </th>
                    <th scope="col" className="app-right">
                      Balance
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((e) => (
                    <tr key={e.seq}>
                      <td className="app-nowrap app-muted">
                        {dateTime(e.created_at)}
                      </td>
                      <td>
                        <span className="app-strong-link">
                          {entryLabel[e.kind]}
                        </span>
                        {e.reference?.startsWith("clm_") ? (
                          <Link
                            href={`/console/claims/${e.reference}`}
                            className="app-muted app-block"
                          >
                            {e.memo}
                          </Link>
                        ) : (
                          <span className="app-muted app-block">{e.memo}</span>
                        )}
                      </td>
                      <td
                        className={`app-right app-nowrap ${e.amount > 0 ? "app-in" : ""}`}
                      >
                        {e.amount > 0 ? "+" : "−"}
                        {money(Math.abs(e.amount), w.currency).replace(
                          /^\w+ /,
                          "",
                        )}
                      </td>
                      <td className="app-right app-nowrap app-muted">
                        {money(e.balance_after, w.currency).replace(
                          /^\w+ /,
                          "",
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {(older || before) && (
                <div className="app-pager">
                  {before && (
                    <Link
                      href="/console/wallet"
                      className="app-button app-button--small"
                    >
                      Newest
                    </Link>
                  )}
                  {older && (
                    <Link
                      href={`/console/wallet?before=${older}`}
                      className="app-button app-button--small"
                    >
                      Older entries
                    </Link>
                  )}
                </div>
              )}
            </>
          ) : (
            <p className="app-empty">
              Nothing yet. Add funds to start; every charge and payment shows
              here.
            </p>
          )}
        </section>

        <div className="app-stack">
          <section className="app-card" aria-labelledby="add-h">
            <h2 id="add-h" className="app-card__title">
              Add funds
            </h2>
            <AddFundsForm providers={providers} suggested={suggested} />
          </section>
          <section className="app-card" aria-labelledby="wd-h">
            <h2 id="wd-h" className="app-card__title">
              Withdraw
            </h2>
            {accounts.length > 0 ? (
              <WithdrawForm accounts={accounts} available={w.available} />
            ) : (
              <p className="app-muted">
                Add the account withdrawals should go to first.
              </p>
            )}
            {withdrawals.length > 0 && (
              <ul className="app-rows">
                {withdrawals.slice(0, 5).map((x) => (
                  <li key={x.id}>
                    <span>
                      {money(x.amount, x.currency)}
                      <span className="app-muted app-block">
                        {x.destination}
                      </span>
                      {x.message && (
                        <span className="app-muted app-block">{x.message}</span>
                      )}
                    </span>
                    <span className={`app-pill ${withdrawalTone[x.status][1]}`}>
                      {withdrawalTone[x.status][0]}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <section className="app-card" aria-labelledby="pa-h">
            <h2 id="pa-h" className="app-card__title">
              Where withdrawals go
            </h2>
            {accounts.length > 0 && (
              <ul className="app-rows">
                {accounts.map((a) => (
                  <li key={a.id}>
                    <span>
                      {a.bank_name || a.bank_code} {a.account_number}
                      <span className="app-muted app-block">{a.name}</span>
                    </span>
                    <form action={removePayoutAccount}>
                      <input type="hidden" name="id" value={a.id} />
                      <Submit className="app-button app-button--quiet app-button--small">
                        Remove
                      </Submit>
                    </form>
                  </li>
                ))}
              </ul>
            )}
            <details className="app-details" open={accounts.length === 0}>
              <summary>Add an account</summary>
              <PayoutAccountForm providers={payers} banks={banks} />
            </details>
          </section>
        </div>
      </div>
    </div>
  );
}
