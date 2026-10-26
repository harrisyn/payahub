"use client";

import { useActionState } from "react";

import { addWebhook, type HookState } from "@/app/actions";
import { CopyButton } from "@/components/copy";
import { environments, Messages, Submit } from "@/components/forms";
import { Select, TextInput } from "@/components/ui";

// WebhookForm adds an endpoint and shows its signing secret, once.
export function WebhookForm() {
  const [s, action] = useActionState(addWebhook, {} as HookState);
  const v = s.values ?? {};
  return (
    <>
      {s.secret && (
        <div className="app-banner app-banner--good app-secret" role="status">
          <span>
            Events now go to <strong>{s.url}</strong>. Your signing secret is
            below. Store it now: it is not shown again.
          </span>
          <code className="app-id">{s.secret}</code>
          <CopyButton value={s.secret} label="Copy secret" />
        </div>
      )}
      <form
        action={action}
        className="app-inline-form"
        noValidate
        key={s.secret ?? JSON.stringify(v)}
      >
        <TextInput
          id="f-hookurl"
          name="url"
          type="url"
          inputMode="url"
          label="Your system's address"
          placeholder="https://hms.example.com/payahub/events"
          defaultValue={String(v.url ?? "")}
          error={s.fields?.url}
          className="app-inline-form__grow"
        />
        <TextInput
          id="f-hookdesc"
          name="description"
          label="Name"
          placeholder="Hospital system"
          defaultValue={String(v.description ?? "")}
        />
        <Select
          id="f-hookenv"
          name="environment"
          label="Environment"
          defaultValue={String(v.environment ?? "sandbox")}
          data={environments}
          className="app-inline-form__env"
        />
        <Submit className="app-button app-button--primary">Add endpoint</Submit>
        <Messages state={{ error: s.error }} />
      </form>
    </>
  );
}
