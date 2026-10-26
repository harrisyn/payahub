"use client";

import { useActionState } from "react";

import { checkMember, type MemberState } from "@/app/actions";
import { environments, Messages, Submit } from "@/components/forms";
import { Select, TextInput } from "@/components/ui";
import { date } from "@/lib/format";
import type { MemberCheck, Payer } from "@/lib/types";

// MemberCheckForm asks an insurer whether a member is covered, the way the
// front desk does before a visit, and walks through the code some insurers
// text to the member first.
export function MemberCheckForm({ payers }: { payers: Payer[] }) {
  const [s, action] = useActionState(checkMember, {} as MemberState);
  const v = s.values ?? {};
  const needsCode =
    s.check?.result?.otp_required || s.check?.action === "send_otp";
  return (
    <form action={action} className="app-form" noValidate>
      <div className="app-form__row app-form__row--wide">
        <Select
          id="m-payer"
          name="payer_id"
          label="Insurer"
          placeholder="Choose the insurer"
          defaultValue={String(v.payer_id ?? "") || null}
          error={s.fields?.payer_id}
          data={payers.map((p) => ({ value: p.id, label: p.name }))}
        />
        <Select
          id="m-env"
          name="environment"
          label="Environment"
          defaultValue={String(v.environment ?? "sandbox")}
          data={environments}
        />
      </div>
      <div className="app-form__row app-form__row--wide">
        <TextInput
          id="m-number"
          name="member_number"
          label="Member number"
          placeholder="From the member's card"
          autoComplete="off"
          defaultValue={String(v.member_number ?? "")}
          error={s.fields?.member_number}
        />
        {needsCode && (
          <TextInput
            id="m-otp"
            name="otp"
            label="Code from the member"
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder="6 digits"
            error={s.fields?.otp}
          />
        )}
      </div>
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">Check cover</Submit>
        {needsCode && (
          <button
            type="submit"
            name="action"
            value="send_otp"
            className="app-button"
          >
            Send the member a code
          </button>
        )}
        <Messages state={{ error: s.fields ? undefined : s.error }} />
      </div>
      {s.check && <Answer check={s.check} />}
    </form>
  );
}

function Answer({ check }: { check: MemberCheck }) {
  const r = check.result;
  const who = [r?.member?.first_name, r?.member?.last_name]
    .filter(Boolean)
    .join(" ");
  if (check.status === "pending")
    return (
      <p className="app-banner" role="status">
        The insurer has not answered yet. Try again in a moment.
      </p>
    );
  if (check.status === "failed")
    return (
      <p className="app-banner app-banner--bad" role="alert">
        {check.error_code === "member_not_found"
          ? `The insurer has no member ${check.member_number}. Check the number on the card.`
          : check.error_message || "The insurer could not answer."}
      </p>
    );
  if (check.action === "send_otp")
    return (
      <p className="app-banner" role="status">
        {r?.challenge || "The insurer sent the member a code."} Ask the member
        for it and check again.
      </p>
    );
  if (r?.otp_required)
    return (
      <p className="app-banner" role="status">
        This insurer needs a code from the member. Send them one, then enter it
        and check again.
      </p>
    );
  if (r?.eligible)
    return (
      <div className="app-banner app-banner--good" role="status">
        <span>
          <strong>Covered.</strong> {who || check.member_number}
          {r.plan_name ? `, ${r.plan_name}` : ""}
          {r.cover_ends ? `, until ${date(r.cover_ends)}` : ""}.
        </span>
      </div>
    );
  return (
    <div className="app-banner app-banner--bad" role="alert">
      <span>
        <strong>Not covered.</strong> {who || check.member_number}
        {r?.plan_name ? `, ${r.plan_name}` : ""}.{" "}
        {r?.message ||
          (r?.reason === "inactive" ? "The cover is not active." : "")}
      </span>
    </div>
  );
}
