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
import { Select, TextInput, UIProvider } from "@/components/ui";
import { roles } from "@/lib/roles";
import type { Role } from "@/lib/types";

// roleOptions lists the roles a member may give; only owners give owner.
const roleOptions = (owner: boolean) =>
  roles
    .filter((r) => owner || r.id !== "owner")
    .map((r) => ({ value: r.id, label: r.name, description: r.does }));

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
      <TextInput
        id="f-invemail"
        name="email"
        type="email"
        autoComplete="off"
        label="Email"
        placeholder="kofi@clinic.com"
        defaultValue={String(v.email ?? "")}
        error={s.fields?.email}
        className="app-inline-form__grow"
      />
      <TextInput
        id="f-invname"
        name="name"
        label="Name"
        placeholder="Kofi Mensah"
        defaultValue={String(v.name ?? "")}
      />
      <Select
        id="f-invrole"
        name="role"
        label="Role"
        defaultValue={String(v.role ?? "claims")}
        data={roleOptions(owner)}
        className="app-inline-form__env"
      />
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
        <Select
          id={`r-${id}`}
          name="role"
          aria-label="Role"
          defaultValue={role}
          data={roleOptions(owner)}
          className="app-member-controls__role"
        />
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
    <UIProvider>
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
    </UIProvider>
  );
}
