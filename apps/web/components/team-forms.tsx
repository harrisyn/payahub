"use client";

import { useActionState } from "react";

import {
  changeRole,
  inviteMember,
  joinTeam,
  removeMember,
  type FormState,
} from "@/app/actions";
import { Field, Messages, Submit } from "@/components/forms";
import { roles } from "@/lib/roles";
import type { Role } from "@/lib/types";

// InviteForm emails someone a link to join. Only owners see the owner role.
export function InviteForm({ owner }: { owner: boolean }) {
  const [s, action] = useActionState(inviteMember, {} as FormState);
  const v = s.values ?? {};
  return (
    <form
      action={action}
      className="app-inline-form"
      noValidate
      key={s.ok ?? JSON.stringify(v)}
    >
      <div
        className={`field field--inline app-grow${s.fields?.email ? " field--error" : ""}`}
      >
        <label htmlFor="f-invemail">Email</label>
        <input
          id="f-invemail"
          name="email"
          type="email"
          autoComplete="off"
          placeholder="kofi@clinic.com"
          defaultValue={String(v.email ?? "")}
          aria-invalid={!!s.fields?.email}
        />
      </div>
      <div className="field field--inline">
        <label htmlFor="f-invname">Name</label>
        <input
          id="f-invname"
          name="name"
          placeholder="Kofi Mensah"
          defaultValue={String(v.name ?? "")}
        />
      </div>
      <div className="field field--inline">
        <label htmlFor="f-invrole">Role</label>
        <select
          id="f-invrole"
          name="role"
          defaultValue={String(v.role ?? "claims")}
        >
          {roles
            .filter((r) => owner || r.id !== "owner")
            .map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
        </select>
      </div>
      <Submit className="app-button app-button--primary">
        Send invitation
      </Submit>
      <Messages state={{ error: s.error, ok: s.ok }} />
    </form>
  );
}

// MemberControls changes a member's role or removes them.
export function MemberControls({
  id,
  role,
  owner,
  self,
}: {
  id: string;
  role: Role;
  owner: boolean;
  self: boolean;
}) {
  const [rs, roleAction] = useActionState(changeRole, {} as FormState);
  const [ds, removeAction] = useActionState(removeMember, {} as FormState);
  const locked = !owner && role === "owner";
  if (locked)
    return (
      <span className="app-muted">Only an owner can change an owner.</span>
    );
  return (
    <div className="app-member-controls">
      <form action={roleAction} className="app-actions">
        <input type="hidden" name="id" value={id} />
        <label className="sr-only" htmlFor={`r-${id}`}>
          Role
        </label>
        <select id={`r-${id}`} name="role" defaultValue={role}>
          {roles
            .filter((r) => owner || r.id !== "owner")
            .map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
        </select>
        <Submit className="app-button">Change</Submit>
      </form>
      <form action={removeAction}>
        <input type="hidden" name="id" value={id} />
        <Submit className="app-button app-button--danger">
          {self ? "Leave" : "Remove"}
        </Submit>
      </form>
      {(rs.error || ds.error) && (
        <p className="form-error app-member-controls__msg" role="alert">
          {rs.error || ds.error}
        </p>
      )}
    </div>
  );
}

export function JoinForm({ token, name }: { token: string; name: string }) {
  const [s, action] = useActionState(joinTeam, {} as FormState);
  return (
    <form action={action} className="auth__form" noValidate>
      <input type="hidden" name="token" value={token} />
      <Field
        label="Your name"
        name="name"
        autoComplete="name"
        required
        defaultValue={name}
        error={s.fields?.name}
      />
      <Field
        label="Choose a password"
        name="password"
        type="password"
        autoComplete="new-password"
        minLength={10}
        required
        hint="At least 10 characters."
        error={s.fields?.password}
      />
      <Submit className="button button--large">Join the team</Submit>
      <Messages state={{ error: s.fields ? undefined : s.error }} />
    </form>
  );
}
