"use client";

import { useActionState, useState } from "react";

import {
  addFunds,
  addPayoutAccount,
  decideWithdrawal,
  requestWithdrawal,
  type FormState,
} from "@/app/actions";
import { Field, Messages, Submit } from "@/components/forms";
import type { Bank, PaymentProvider, PayoutAccount } from "@/lib/types";

const QUICK = [200, 500, 1000, 2000];

// AddFundsForm opens a checkout at the provider for an amount in cedis.
export function AddFundsForm({
  providers,
  suggested,
}: {
  providers: PaymentProvider[];
  suggested?: number;
}) {
  const [s, action] = useActionState(addFunds, {} as FormState);
  const [amount, setAmount] = useState(suggested ? String(suggested) : "");
  if (!providers.length) {
    return (
      <p className="app-muted">
        No payment provider is set up yet. Contact Payahub to fund your wallet.
      </p>
    );
  }
  return (
    <form action={action} className="app-form" noValidate>
      <Field
        label="Amount (GHS)"
        name="amount"
        inputMode="decimal"
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
        error={s.fields?.amount}
        placeholder="500.00"
        required
      />
      <div className="app-chips" role="group" aria-label="Quick amounts">
        {QUICK.map((q) => (
          <button
            type="button"
            key={q}
            className="app-chip"
            aria-pressed={amount === String(q)}
            onClick={() => setAmount(String(q))}
          >
            GHS {q.toLocaleString("en-GH")}
          </button>
        ))}
      </div>
      {providers.length > 1 ? (
        <div className="field">
          <label htmlFor="f-provider">Pay with</label>
          <select
            id="f-provider"
            name="provider"
            defaultValue={providers[0].id}
            aria-invalid={!!s.fields?.provider}
          >
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          {s.fields?.provider && (
            <p className="field__error">{s.fields.provider}</p>
          )}
        </div>
      ) : (
        <input type="hidden" name="provider" value={providers[0].id} />
      )}
      <p className="app-muted">
        You pay by card or mobile money on{" "}
        {providers.length === 1 ? providers[0].name : "the provider"}&apos;s
        secure page, then come back here. The money is in your wallet as soon as
        the payment is confirmed.
      </p>
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">
          Continue to payment
        </Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

export function PayoutAccountForm({
  providers,
  banks,
}: {
  providers: PaymentProvider[];
  banks: Record<string, Bank[]>;
}) {
  const [s, action] = useActionState(addPayoutAccount, {} as FormState);
  const [provider, setProvider] = useState(providers[0]?.id ?? "");
  const [type, setType] = useState<"mobile_money" | "bank">("mobile_money");
  const options = (banks[provider] ?? []).filter((b) => b.type === type);
  const err = (k: string) => s.fields?.[k];
  if (!providers.length)
    return (
      <p className="app-muted">No provider that pays out is set up yet.</p>
    );
  return (
    <form action={action} className="app-form" noValidate>
      {providers.length > 1 ? (
        <div className="field">
          <label htmlFor="f-pprovider">Paid through</label>
          <select
            id="f-pprovider"
            name="provider"
            value={provider}
            onChange={(e) => setProvider(e.target.value)}
          >
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>
      ) : (
        <input type="hidden" name="provider" value={provider} />
      )}
      <fieldset className="app-segment">
        <legend className="sr-only">Account type</legend>
        {(["mobile_money", "bank"] as const).map((t) => (
          <label key={t} className="app-segment__opt">
            <input
              type="radio"
              name="type"
              value={t}
              checked={type === t}
              onChange={() => setType(t)}
            />
            <span>{t === "bank" ? "Bank account" : "Mobile money"}</span>
          </label>
        ))}
      </fieldset>
      <div className="field">
        <label htmlFor="f-bank">{type === "bank" ? "Bank" : "Network"}</label>
        <select
          id="f-bank"
          name="bank_code"
          key={`${provider}-${type}`}
          defaultValue=""
          aria-invalid={!!err("bank_code")}
        >
          <option value="" disabled>
            {type === "bank" ? "Choose the bank" : "Choose the network"}
          </option>
          {options.map((b) => (
            <option key={b.code} value={b.code}>
              {b.name}
            </option>
          ))}
        </select>
        {err("bank_code") && <p className="field__error">{err("bank_code")}</p>}
      </div>
      <div className="app-form__row">
        <Field
          label={type === "bank" ? "Account number" : "Mobile money number"}
          name="account_number"
          inputMode="numeric"
          autoComplete="off"
          error={err("account_number")}
          placeholder={type === "bank" ? "1002003004" : "024 555 0101"}
        />
        <Field
          label="Name on the account"
          name="name"
          error={err("name")}
          placeholder="Ridge Clinic Ltd"
        />
      </div>
      <div className="app-form__foot">
        <Submit className="app-button">Add account</Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

export function WithdrawForm({
  accounts,
  available,
}: {
  accounts: PayoutAccount[];
  available: number;
}) {
  const [s, action] = useActionState(requestWithdrawal, {} as FormState);
  const err = (k: string) => s.fields?.[k];
  return (
    <form action={action} className="app-form" noValidate>
      <div className="field">
        <label htmlFor="f-pa">Pay to</label>
        <select
          id="f-pa"
          name="payout_account_id"
          defaultValue={accounts[0]?.id}
          aria-invalid={!!err("payout_account_id")}
        >
          {accounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.bank_name || a.bank_code} {a.account_number} ({a.name})
            </option>
          ))}
        </select>
        {err("payout_account_id") && (
          <p className="field__error">{err("payout_account_id")}</p>
        )}
      </div>
      <div className="app-form__row">
        <Field
          label="Amount (GHS)"
          name="amount"
          inputMode="decimal"
          error={err("amount")}
          hint={`Up to GHS ${(available / 100).toLocaleString("en-GH", { minimumFractionDigits: 2 })} available.`}
          placeholder="250.00"
        />
        <Field
          label="Note (optional)"
          name="reason"
          placeholder="Month-end transfer"
        />
      </div>
      <div className="app-form__foot">
        <Submit className="app-button">Request withdrawal</Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

// DecideWithdrawal is an operator's approve or reject, with a reason for a
// rejection that the organisation sees.
export function DecideWithdrawal({ id }: { id: string }) {
  const [s, action] = useActionState(decideWithdrawal, {} as FormState);
  const [rejecting, setRejecting] = useState(false);
  if (s.ok) return <p className="form-ok">{s.ok}</p>;
  return (
    <div className="app-decide">
      {!rejecting ? (
        <form action={action} className="app-inline-form">
          <input type="hidden" name="id" value={id} />
          <input type="hidden" name="decision" value="approve" />
          <Submit className="app-button app-button--primary">
            Approve and pay
          </Submit>
          <button
            type="button"
            className="app-button"
            onClick={() => setRejecting(true)}
          >
            Reject
          </button>
        </form>
      ) : (
        <form action={action} className="app-inline-form" noValidate>
          <input type="hidden" name="id" value={id} />
          <input type="hidden" name="decision" value="reject" />
          <div className="field field--inline">
            <label htmlFor={`r-${id}`}>Why (they see this)</label>
            <input
              id={`r-${id}`}
              name="reason"
              aria-invalid={!!s.fields?.reason}
              placeholder="Confirm the account by phone first"
            />
          </div>
          <Submit className="app-button">Reject</Submit>
          <button
            type="button"
            className="app-button app-button--quiet"
            onClick={() => setRejecting(false)}
          >
            Cancel
          </button>
        </form>
      )}
      <Messages state={s} />
    </div>
  );
}
