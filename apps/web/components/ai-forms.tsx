"use client";

import { useActionState, useState } from "react";

import { saveAIProvider, type FormState } from "@/app/actions";
import { Field, Messages, Submit } from "@/components/forms";
import { Select } from "@/components/ui";
import { taskName } from "@/lib/ai";
import type { AIKind, AIProvider } from "@/lib/types";

const kinds: { id: AIKind; label: string }[] = [
  { id: "anthropic", label: "Claude" },
  { id: "openai", label: "OpenAI" },
  { id: "openai_compatible", label: "Compatible endpoint" },
];

// AIProviderForm adds or edits one model provider. The key is write-only:
// the form never holds the saved one, and an empty field keeps it.
export function AIProviderForm({
  provider,
  defaultModel,
  tasks,
}: {
  provider?: AIProvider;
  defaultModel: string;
  tasks: string[];
}) {
  const [s, action] = useActionState(saveAIProvider, {} as FormState);
  const v = s.values;
  const [kind, setKind] = useState<AIKind>(provider?.kind ?? "anthropic");
  const [model, setModel] = useState(provider?.model ?? defaultModel);
  // A failed save resets the form; put back what was typed (never the key).
  const [seen, setSeen] = useState(v);
  if (v !== seen) {
    setSeen(v);
    if (v) {
      setKind(v.kind as AIKind);
      setModel(String(v.model ?? ""));
    }
  }
  const val = (k: string, d: string) => (v ? String(v[k] ?? "") : d);
  const err = (k: string) => s.fields?.[k];
  const chosen = new Set(
    v ? (v.tasks as string[]) : (provider?.tasks ?? tasks),
  );
  const pick = (k: AIKind) => {
    setKind(k);
    if (!provider) setModel(k === "anthropic" ? defaultModel : "");
  };
  return (
    <form
      action={action}
      className="app-form"
      noValidate
      key={v ? JSON.stringify(v) : "fresh"}
    >
      <fieldset className="app-segment">
        <legend className="sr-only">Provider</legend>
        {kinds.map((k) => (
          <label key={k.id} className="app-segment__opt">
            <input
              type="radio"
              name="kind"
              value={k.id}
              checked={kind === k.id}
              onChange={() => pick(k.id)}
            />
            <span>{k.label}</span>
          </label>
        ))}
      </fieldset>
      {err("kind") && <p className="field__error">{err("kind")}</p>}
      <div className="app-form__row">
        <Field
          label="Name"
          name="name"
          defaultValue={val("name", provider?.name ?? "")}
          error={err("name")}
          placeholder={
            kind === "anthropic"
              ? "Claude"
              : kind === "openai"
                ? "OpenAI"
                : "Local model"
          }
        />
        {provider ? (
          <input type="hidden" name="id" value={provider.id} />
        ) : (
          <Field
            label="Short id"
            name="id"
            autoComplete="off"
            defaultValue={val("id", "")}
            error={err("id")}
            hint="Used in logs, like claude or openai-backup."
            placeholder={
              kind === "anthropic"
                ? "claude"
                : kind === "openai"
                  ? "openai"
                  : "local"
            }
          />
        )}
      </div>
      <div className="app-form__row">
        <Field
          label="Model"
          name="model"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          error={err("model")}
          autoComplete="off"
          placeholder={kind === "anthropic" ? defaultModel : "gpt-5"}
        />
        {kind === "anthropic" ? (
          <Select
            id="f-effort"
            name="effort"
            label="Effort"
            defaultValue={val("effort", provider?.effort || "") || "high"}
            error={err("effort")}
            data={[
              { value: "low", label: "Low, fastest" },
              { value: "medium", label: "Medium" },
              { value: "high", label: "High" },
              { value: "xhigh", label: "Extra high" },
              { value: "max", label: "Max, slowest" },
            ]}
          />
        ) : (
          <Select
            id="f-json"
            name="json_mode"
            label="Structured answers"
            defaultValue={
              val("json_mode", provider?.json_mode || "") || "schema"
            }
            error={err("json_mode")}
            data={[
              { value: "schema", label: "JSON schema (strict)" },
              { value: "object", label: "JSON object" },
              { value: "none", label: "Plain text, parsed" },
            ]}
          />
        )}
      </div>
      <Field
        label={
          kind === "openai_compatible" ? "Base URL" : "Base URL (optional)"
        }
        name="base_url"
        inputMode="url"
        autoComplete="off"
        defaultValue={val("base_url", provider?.base_url ?? "")}
        error={err("base_url")}
        hint={
          kind === "openai_compatible"
            ? "Where /chat/completions lives, like https://models.example.com/v1."
            : "Leave empty to use the provider's own API."
        }
        placeholder={
          kind === "openai_compatible" ? "https://models.example.com/v1" : ""
        }
      />
      <Field
        label="API key"
        name="api_key"
        type="password"
        autoComplete="off"
        error={err("api_key")}
        hint={
          provider?.has_key
            ? `A key ending ${provider.key_hint} is saved. Leave empty to keep it.`
            : kind === "openai_compatible"
              ? "Only if the endpoint asks for one. It is sealed and never shown again."
              : "Sealed on save and never shown again."
        }
      />
      <fieldset className="app-fieldset">
        <legend>Used for</legend>
        <div className="app-choices">
          {tasks.map((t) => (
            <label key={t} className="app-choice">
              <input
                type="checkbox"
                name="tasks"
                value={t}
                defaultChecked={chosen.has(t)}
              />
              <span>
                {taskName[t]?.[0] ?? t}
                <span className="app-muted app-block">{taskName[t]?.[1]}</span>
              </span>
            </label>
          ))}
        </div>
        {err("tasks") && <p className="field__error">{err("tasks")}</p>}
      </fieldset>
      <div className="app-form__row">
        <Field
          label="Order"
          name="priority"
          inputMode="numeric"
          defaultValue={val("priority", String(provider?.priority ?? 10))}
          error={err("priority")}
          hint="Lower is tried first; the next takes over when one is down."
        />
        <label className="app-switchrow">
          <input
            type="checkbox"
            name="enabled"
            defaultChecked={
              v ? v.enabled === true : (provider?.enabled ?? true)
            }
          />
          <span>
            <span className="app-step__title">In use</span>
            <span className="app-muted">
              Turn off to skip it without losing its settings.
            </span>
          </span>
        </label>
      </div>
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">
          {provider ? "Save changes" : "Add provider"}
        </Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}
