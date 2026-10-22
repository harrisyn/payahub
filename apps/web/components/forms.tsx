"use client";

import { useActionState } from "react";
import { useFormStatus } from "react-dom";

import {
  checkAccount,
  choosePlan,
  createKey,
  saveAccount,
  saveMappings,
  savePayerUse,
  saveProfile,
  signIn,
  signUp,
  suggestMappings,
  type FormState,
} from "@/app/actions";
import { itemLabel } from "@/lib/format";
import { ghs, plans, TRIAL_DAYS, TRIAL_LIVE_CLAIMS } from "@/lib/pricing";
import type {
  Connector,
  ConnectorAccount,
  CredentialField,
  Payer,
  PayerSetting,
  Profile,
} from "@/lib/types";

export function Submit({ children, className = "button" }: { children: React.ReactNode; className?: string }) {
  const { pending } = useFormStatus();
  return (
    <button type="submit" className={className} disabled={pending} aria-busy={pending}>
      {pending ? "Working…" : children}
    </button>
  );
}

function Messages({ state }: { state: FormState }) {
  return (
    <>
      {state.error && (
        <p className="form-error" role="alert">
          {state.error}
        </p>
      )}
      {state.ok && (
        <p className="form-ok" role="status">
          {state.ok}
        </p>
      )}
    </>
  );
}

export function Field({
  label,
  name,
  type = "text",
  error,
  hint,
  ...rest
}: {
  label: string;
  name: string;
  type?: string;
  error?: string;
  hint?: string;
} & React.InputHTMLAttributes<HTMLInputElement>) {
  const id = `f-${name}`;
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        name={name}
        type={type}
        aria-invalid={!!error}
        aria-describedby={error ? `${id}-e` : hint ? `${id}-h` : undefined}
        {...rest}
      />
      {hint && !error && (
        <p className="field__hint" id={`${id}-h`}>
          {hint}
        </p>
      )}
      {error && (
        <p className="field__error" id={`${id}-e`}>
          {error}
        </p>
      )}
    </div>
  );
}

export function SignUpForm() {
  const [s, action] = useActionState(signUp, {});
  return (
    <form action={action} className="auth__form" noValidate>
      <Field label="Your name" name="name" autoComplete="name" required error={s.fields?.name} />
      <Field label="Work email" name="email" type="email" autoComplete="email" required error={s.fields?.email} />
      <Field
        label="Organisation"
        name="organization"
        autoComplete="organization"
        required
        hint="Your clinic, hospital, pharmacy or software company."
        error={s.fields?.organization}
      />
      <div className="field">
        <label htmlFor="f-country">Country</label>
        <select id="f-country" name="country" defaultValue="GH">
          <option value="GH">Ghana</option>
          <option value="NG">Nigeria</option>
          <option value="KE">Kenya</option>
          <option value="US">United States</option>
        </select>
      </div>
      <Field
        label="Password"
        name="password"
        type="password"
        autoComplete="new-password"
        minLength={10}
        required
        hint="At least 10 characters."
        error={s.fields?.password}
      />
      <Submit className="button button--large">Create my account</Submit>
      <p className="auth__alt">
        {TRIAL_DAYS} days and {TRIAL_LIVE_CLAIMS} live claims free. No card.
      </p>
      <Messages state={{ error: s.fields ? undefined : s.error }} />
    </form>
  );
}

export function SignInForm() {
  const [s, action] = useActionState(signIn, {});
  return (
    <form action={action} className="auth__form" noValidate>
      <Field label="Work email" name="email" type="email" autoComplete="email" required />
      <Field label="Password" name="password" type="password" autoComplete="current-password" required />
      <Submit className="button button--large">Sign in</Submit>
      <Messages state={s} />
    </form>
  );
}

const billing = [
  { v: "emr", l: "An EMR or hospital system" },
  { v: "practice_software", l: "Practice or pharmacy software" },
  { v: "spreadsheets", l: "Spreadsheets" },
  { v: "paper", l: "Paper forms" },
];
const leave = [
  { v: "portal", l: "Typed into each insurer's portal" },
  { v: "email", l: "Emailed as files" },
  { v: "paper", l: "Delivered on paper" },
  { v: "integration", l: "Sent by an existing integration" },
];

export function ProfileForm({ profile, payers }: { profile: Profile | null; payers: Payer[] }) {
  const [s, action] = useActionState(saveProfile, {});
  const chosen = new Set(profile?.payers ?? []);
  return (
    <form action={action} className="app-form" noValidate>
      <fieldset className="app-fieldset">
        <legend>Where do your bills live today?</legend>
        <div className="app-choices">
          {billing.map((o) => (
            <label key={o.v} className="app-choice">
              <input type="radio" name="billing_system" value={o.v} defaultChecked={profile?.billing_system === o.v} required />
              {o.l}
            </label>
          ))}
        </div>
        {s.fields?.billing_system && <p className="field__error">Choose one.</p>}
      </fieldset>
      <fieldset className="app-fieldset">
        <legend>How do claims reach insurers now?</legend>
        <div className="app-choices">
          {leave.map((o) => (
            <label key={o.v} className="app-choice">
              <input type="radio" name="claims_leave_by" value={o.v} defaultChecked={profile?.claims_leave_by === o.v} required />
              {o.l}
            </label>
          ))}
        </div>
        {s.fields?.claims_leave_by && <p className="field__error">Choose one.</p>}
      </fieldset>
      <div className="app-row2">
        <Field
          label="Claims a month, roughly"
          name="monthly_claims"
          type="number"
          min={0}
          inputMode="numeric"
          defaultValue={profile?.monthly_claims ?? ""}
          error={s.fields?.monthly_claims}
        />
        <fieldset className="app-fieldset">
          <legend>Do you have developers, in-house or hired?</legend>
          <div className="app-choices app-choices--inline">
            <label className="app-choice">
              <input type="radio" name="has_developers" value="yes" defaultChecked={profile?.has_developers === true} />
              Yes
            </label>
            <label className="app-choice">
              <input type="radio" name="has_developers" value="no" defaultChecked={profile ? !profile.has_developers : false} />
              No
            </label>
          </div>
        </fieldset>
      </div>
      <fieldset className="app-fieldset">
        <legend>Which insurers do you bill?</legend>
        <div className="app-choices app-choices--grid">
          {payers.map((p) => (
            <label key={p.id} className="app-choice">
              <input type="checkbox" name="payers" value={p.id} defaultChecked={chosen.has(p.id)} />
              <span>
                {p.name}
                {p.kind === "integrated" && <span className="app-muted"> through {p.connector.name}</span>}
              </span>
            </label>
          ))}
        </div>
      </fieldset>
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">Save and recommend</Submit>
        <Messages state={{ error: s.error, ok: s.ok }} />
      </div>
    </form>
  );
}

export function PayerUseForm({ payer, env, setting }: { payer: Payer; env: "sandbox" | "live"; setting?: PayerSetting }) {
  const [s, action] = useActionState(savePayerUse, {});
  const platform = payer.connector.name;
  return (
    <form action={action} className="app-form" noValidate>
      <input type="hidden" name="payer" value={payer.id} />
      <input type="hidden" name="environment" value={env} />
      <label className="app-switchrow">
        <input type="checkbox" name="enabled" defaultChecked={setting?.enabled ?? true} />
        <span>
          <span className="app-step__title">
            Send {env} claims to {payer.name}
          </span>
          <span className="app-muted">Turn off to stop new claims; your account stays saved.</span>
        </span>
      </label>
      {payer.kind === "integrated" && (
        <fieldset className="app-fieldset">
          <legend>
            {payer.name} is reached through {platform}. Do you have a {platform} account?
          </legend>
          <div className="app-choices app-choices--inline">
            <label className="app-choice">
              <input type="radio" name="account" value="have" defaultChecked={setting?.account !== "need"} />
              Yes
            </label>
            <label className="app-choice">
              <input type="radio" name="account" value="need" defaultChecked={setting?.account === "need"} />
              Not yet
            </label>
          </div>
        </fieldset>
      )}
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">Save</Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

export function AccountForm({
  connector,
  env,
  account,
}: {
  connector: Connector;
  env: "sandbox" | "live";
  account?: ConnectorAccount;
}) {
  const [s, action] = useActionState(saveAccount, {});
  const set = new Set(account?.credential_fields ?? []);
  return (
    <form action={action} className="app-form" noValidate autoComplete="off">
      <input type="hidden" name="connector" value={connector.id} />
      <input type="hidden" name="environment" value={env} />
      <div className="app-creds">
        {connector.credentials.map((f: CredentialField) => (
          <Field
            key={f.name}
            label={f.label + (f.required ? "" : " (optional)")}
            name={`cred_${f.name}`}
            type={f.secret ? "password" : "text"}
            autoComplete="off"
            placeholder={set.has(f.name) ? "Saved. Type to replace" : ""}
          />
        ))}
      </div>
      <p className="app-muted">
        To change one field, re-enter them all: credentials are sealed together and never shown again.
      </p>
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">Save account</Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

export function CheckForm({ connector, env, show = false }: { connector: string; env: string; show?: boolean }) {
  const [s, action] = useActionState(checkAccount, {});
  return (
    <form action={action} className="app-inline-form">
      <input type="hidden" name="connector" value={connector} />
      <input type="hidden" name="environment" value={env} />
      {show && <input type="hidden" name="show" value="1" />}
      <Submit className="app-button">Check connection</Submit>
      <Messages state={s} />
    </form>
  );
}

// MappingForm turns a pasted price list into suggested standard items, which
// a person confirms line by line before anything is saved.
export function MappingForm() {
  const [found, suggest] = useActionState(suggestMappings, {});
  const [saved, save] = useActionState(saveMappings, {});
  return (
    <div className="app-stack">
      <form action={suggest} className="app-form" noValidate>
        <div className="field">
          <label htmlFor="f-list">Your items, one per line: your code, then its name</label>
          <textarea
            id="f-list"
            name="list"
            rows={6}
            className="app-textarea"
            placeholder={"CONS, OPD consultation\nPCM500, Paracetamol 500mg tabs\nFBC, Full blood count"}
          />
        </div>
        <div className="app-form__foot">
          <Submit className="app-button app-button--primary">Suggest matches</Submit>
          <Messages state={{ error: found.error }} />
        </div>
      </form>
      {found.rows && (
        <form action={save} className="app-form" noValidate>
          <table className="app-table">
            <thead>
              <tr>
                <th scope="col">Your code</th>
                <th scope="col">Your name</th>
                <th scope="col">Standard item</th>
              </tr>
            </thead>
            <tbody>
              {found.rows.map((r, i) => (
                <tr key={`${r.code}-${i}`}>
                  <td className="app-id">
                    {r.code}
                    <input type="hidden" name="row" value={i} />
                    <input type="hidden" name={`code_${i}`} value={r.code} />
                    <input type="hidden" name={`name_${i}`} value={r.name} />
                  </td>
                  <td>{r.name}</td>
                  <td>
                    <label className="sr-only" htmlFor={`f-item-${i}`}>
                      Standard item for {r.code}
                    </label>
                    <select id={`f-item-${i}`} name={`item_${i}`} defaultValue={r.suggestions[0]?.confidence >= 0.6 ? r.suggestions[0].item.id : ""}>
                      <option value="">Skip for now</option>
                      {r.suggestions.map((sg) => (
                        <option key={sg.item.id} value={sg.item.id}>
                          {itemLabel(sg.item)}, {Math.round(sg.confidence * 100)}% match
                        </option>
                      ))}
                    </select>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="app-muted">
            Matches of 60% or more are picked for you; check each one. Nothing is used on a claim until you save it.
          </p>
          <div className="app-form__foot">
            <Submit className="app-button app-button--primary">Save these mappings</Submit>
            <Messages state={saved} />
          </div>
        </form>
      )}
    </div>
  );
}

export function KeyForm() {
  const [s, action] = useActionState(createKey, {});
  return (
    <form action={action} className="app-inline-form" noValidate>
      <div className="field field--inline">
        <label htmlFor="f-keyname">Name</label>
        <input id="f-keyname" name="name" placeholder="Front desk system" />
      </div>
      <div className="field field--inline">
        <label htmlFor="f-keyenv">Environment</label>
        <select id="f-keyenv" name="environment" defaultValue="sandbox">
          <option value="sandbox">Sandbox</option>
          <option value="live">Live</option>
        </select>
      </div>
      <Submit className="app-button app-button--primary">Create key</Submit>
      <Messages state={{ error: s.error }} />
    </form>
  );
}

export function PlanForm({ current, claims }: { current: string; claims: number }) {
  const [s, action] = useActionState(choosePlan, {});
  return (
    <form action={action} className="app-form" noValidate>
      <fieldset className="app-fieldset">
        <legend>Choose how to pay after the trial</legend>
        <div className="app-plans">
          {plans.map((p) => {
            const fee = p.monthlyFee ? `${ghs(p.monthlyFee)} a month` : "No monthly fee";
            const per = p.perClaim ? `${ghs(p.perClaim)} per acknowledged claim` : p.id === "flat" ? `Up to ${p.included.toLocaleString("en-GH")} claims` : "";
            return (
              <label key={p.id} className="app-plan">
                <input
                  type="radio"
                  name="plan"
                  value={p.id}
                  defaultChecked={current === p.id || (current === "enterprise_requested" && p.id === "enterprise")}
                />
                <span className="app-plan__name">{p.name}</span>
                <span className="app-muted">{p.id === "enterprise" ? "Agreed with you, invoiced offline" : `${fee}. ${per}`}</span>
              </label>
            );
          })}
        </div>
      </fieldset>
      <p className="app-muted">
        At this month&apos;s {claims.toLocaleString("en-GH")} billable claims. Payment by card or mobile money is set up when
        billing opens; choosing a plan now lifts the trial&apos;s live-claim cap.
      </p>
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">Choose this plan</Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

