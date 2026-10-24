"use client";

import { useActionState } from "react";

import { addWebhook, type HookState } from "@/app/actions";
import { CopyButton } from "@/components/copy";
import { Messages, Submit } from "@/components/forms";

// WebhookForm adds an endpoint and shows its signing secret, once.
export function WebhookForm() {
  const [s, action] = useActionState(addWebhook, {} as HookState);
  const v = s.values ?? {};
  return (
    <>
      {s.secret && (
        <div className="app-banner app-banner--good app-secret" role="status">
          <span>
            Events now go to <strong>{s.url}</strong>. Your signing secret is below. Store it now: it is not shown again.
          </span>
          <code className="app-id">{s.secret}</code>
          <CopyButton value={s.secret} label="Copy secret" />
        </div>
      )}
      <form action={action} className="app-inline-form" noValidate key={s.secret ?? JSON.stringify(v)}>
        <div className={`field field--inline app-grow${s.fields?.url ? " field--error" : ""}`}>
          <label htmlFor="f-hookurl">Your system&rsquo;s address</label>
          <input
            id="f-hookurl"
            name="url"
            type="url"
            inputMode="url"
            placeholder="https://hms.example.com/payahub/events"
            defaultValue={String(v.url ?? "")}
            aria-invalid={!!s.fields?.url}
          />
        </div>
        <div className="field field--inline">
          <label htmlFor="f-hookdesc">Name</label>
          <input id="f-hookdesc" name="description" placeholder="Hospital system" defaultValue={String(v.description ?? "")} />
        </div>
        <div className="field field--inline">
          <label htmlFor="f-hookenv">Environment</label>
          <select id="f-hookenv" name="environment" defaultValue={String(v.environment ?? "sandbox")}>
            <option value="sandbox">Sandbox</option>
            <option value="live">Live</option>
          </select>
        </div>
        <Submit className="app-button app-button--primary">Add endpoint</Submit>
        <Messages state={{ error: s.error }} />
      </form>
    </>
  );
}
