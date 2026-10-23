import Link from "next/link";

import { checkAIProvider, deleteAIProvider } from "@/app/actions";
import { AIProviderForm } from "@/components/ai-forms";
import { Submit } from "@/components/forms";
import { taskName } from "@/lib/ai";
import { api } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { requireOperator } from "@/lib/session";
import type { AIProvider } from "@/lib/types";

export const metadata = { title: "AI models" };

const kindName: Record<AIProvider["kind"], string> = {
  anthropic: "Claude",
  openai: "OpenAI",
  openai_compatible: "Compatible endpoint",
};

export default async function AIModels({
  searchParams,
}: {
  searchParams: Promise<{ edit?: string; saved?: string }>;
}) {
  await requireOperator();
  const { edit, saved } = await searchParams;
  const res = await api<{
    data: AIProvider[];
    tasks: string[];
    default_models: Record<string, string>;
  }>("/v1/operator/ai-providers");
  if (!res.ok) throw new Error(res.error.message);
  const { data: providers, tasks, default_models } = res.data;
  const editing = edit ? providers.find((p) => p.id === edit) : undefined;
  const justSaved = saved ? providers.find((p) => p.id === saved) : undefined;
  // For each task, the providers that serve it in the order they are tried.
  const chains = tasks.map((t) => ({
    task: t,
    order: providers
      .filter((p) => p.enabled && p.tasks.includes(t))
      .sort((a, b) => a.priority - b.priority),
  }));

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>AI models</h1>
          <p className="app-finding">
            {providers.length
              ? "Each task tries its models in order and moves to the next when one is down. Nothing a model writes goes to an insurer unchecked."
              : "No model is set up, so documents are read by people. Add Claude, OpenAI or any compatible endpoint."}
          </p>
        </div>
      </div>

      {justSaved && (
        <div className="app-banner app-banner--good" role="status">
          {justSaved.name} saved. Run a check to confirm the key and model
          answer.
        </div>
      )}

      {providers.length > 0 && (
        <section className="app-card" aria-labelledby="order-h">
          <h2 id="order-h" className="app-card__title">
            Who answers each task
          </h2>
          <dl className="app-chains">
            {chains.map((c) => (
              <div key={c.task}>
                <dt>{taskName[c.task]?.[0] ?? c.task}</dt>
                <dd>
                  {c.order.length ? (
                    <ol className="app-chain">
                      {c.order.map((p) => (
                        <li key={p.id}>
                          {p.name} <span className="app-muted">{p.model}</span>
                        </li>
                      ))}
                    </ol>
                  ) : (
                    <span className="app-muted">
                      No model; handled by people.
                    </span>
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      )}

      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="prov-h">
          <div className="app-card__head">
            <h2 id="prov-h" className="app-card__title">
              Providers
            </h2>
            {editing && (
              <Link
                href="/console/operator/ai"
                className="app-button app-button--small"
              >
                Add another
              </Link>
            )}
          </div>
          {providers.length ? (
            <ul className="app-rows app-providers">
              {providers.map((p) => (
                <li
                  key={p.id}
                  aria-current={p.id === editing?.id ? "true" : undefined}
                >
                  <span>
                    <span className="app-strong-link">{p.name}</span>{" "}
                    <span className="app-muted">
                      {kindName[p.kind]} · {p.model}
                      {p.kind === "anthropic" && p.effort
                        ? ` · ${p.effort} effort`
                        : ""}
                    </span>
                    <span className="app-muted app-block">
                      {p.base_url || "Provider's own API"} · order {p.priority}
                      {p.has_key ? ` · key …${p.key_hint}` : " · no key"}
                    </span>
                    {p.checked_at && (
                      <span
                        className={`app-block ${p.check_ok ? "app-muted" : "field__error"}`}
                      >
                        Checked {dateTime(p.checked_at)}: {p.check_message}
                      </span>
                    )}
                  </span>
                  <span className="app-provider__side">
                    <span
                      className={`app-pill ${!p.enabled ? "app-pill--neutral" : p.check_ok === undefined ? "app-pill--wait" : p.check_ok ? "app-pill--good" : "app-pill--bad"}`}
                    >
                      {!p.enabled
                        ? "Off"
                        : p.check_ok === undefined
                          ? "Not checked"
                          : p.check_ok
                            ? "Working"
                            : "Failing"}
                    </span>
                    <span className="app-inline-form">
                      <form action={checkAIProvider}>
                        <input type="hidden" name="id" value={p.id} />
                        <Submit className="app-button app-button--small">
                          Check
                        </Submit>
                      </form>
                      <Link
                        href={`/console/operator/ai?edit=${p.id}`}
                        className="app-button app-button--quiet app-button--small"
                      >
                        Edit
                      </Link>
                    </span>
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="app-empty">No providers yet.</p>
          )}
        </section>

        <section className="app-card" aria-labelledby="form-h">
          <h2 id="form-h" className="app-card__title">
            {editing ? `Edit ${editing.name}` : "Add a provider"}
          </h2>
          <AIProviderForm
            key={editing?.id ?? "new"}
            provider={editing}
            defaultModel={default_models.anthropic}
            tasks={tasks}
          />
          {editing && (
            <form action={deleteAIProvider} className="app-form__foot">
              <input type="hidden" name="id" value={editing.id} />
              <Submit className="app-button app-button--quiet app-button--small">
                Remove {editing.name}
              </Submit>
            </form>
          )}
        </section>
      </div>
    </div>
  );
}
