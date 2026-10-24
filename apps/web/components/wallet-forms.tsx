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
import { Select, TextInput } from "@/components/ui";
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
        <Select
          id="f-provider"
          name="provider"
          label="Pay with"
          defaultValue={providers[0].id}
          error={s.fields?.provider}
          data={providers.map((p) => ({ value: p.id, label: p.name }))}
        />
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
        <Select
          id="f-pprovider"
          name="provider"
          label="Paid through"
          value={provider}
          onChange={(v) => v && setProvider(v)}
          data={providers.map((p) => ({ value: p.id, label: p.name }))}
        />
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
      <Select
        id="f-bank"
        name="bank_code"
        key={`${provider}-${type}`}
        label={type === "bank" ? "Bank" : "Network"}
        placeholder={type === "bank" ? "Choose the bank" : "Choose the network"}
        error={err("bank_code")}
        data={options.map((b) => ({ value: b.code, label: b.name }))}
      />
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
      <Select
        id="f-pa"
        name="payout_account_id"
        label="Pay to"
        defaultValue={accounts[0]?.id}
        error={err("payout_account_id")}
        data={accounts.map((a) => ({
          value: a.id,
          label: `${a.bank_name || a.bank_code} ${a.account_number}`,
          description: a.name,
        }))}
      />
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
          <TextInput
            id={`r-${id}`}
            name="reason"
            label="Why (they see this)"
            error={s.fields?.reason}
            placeholder="Confirm the account by phone first"
            className="app-inline-form__grow"
          />
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
