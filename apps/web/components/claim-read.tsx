"use client";

import { startTransition, useActionState, useEffect, useState } from "react";

import { readClaimForm, saveReadClaim, type ReadState } from "@/app/actions";
import { Messages, Submit } from "@/components/forms";
import type { Extraction, Payer, ReadField } from "@/lib/types";

const pct = (n: number) => `${Math.round(n * 100)}%`;

// What the person sees under a value the model read: nothing when it is
// sure, a prompt to check it with the words it came from when it is not.
function Check({ f, error }: { f?: ReadField; error?: string }) {
  if (error) return <p className="field__error">{error}</p>;
  if (!f?.needs_review) return null;
  return (
    <p className="field__check">
      <span className="app-pill app-pill--wait">Check</span>{" "}
      {f.read ? (
        <>Read as &ldquo;{f.read}&rdquo;. </>
      ) : f.quote ? (
        <>
          From &ldquo;{f.quote}&rdquo;, {pct(f.confidence)} sure.{" "}
        </>
      ) : f.confidence > 0 ? (
        <>{pct(f.confidence)} sure. </>
      ) : null}
      {f.note}
    </p>
  );
}

function RField({
  label,
  name,
  path,
  x,
  s,
  value,
  type = "text",
  ...rest
}: {
  label: string;
  name: string;
  path: string;
  x: Extraction;
  s: ReadState;
  value?: string | number;
  type?: string;
} & React.InputHTMLAttributes<HTMLInputElement>) {
  const f = x.fields[path];
  const error = s.fields?.[path];
  return (
    <div className={`field${f?.needs_review || error ? " field--check" : ""}`}>
      <label htmlFor={`r-${name}`}>{label}</label>
      <input
        id={`r-${name}`}
        name={name}
        type={type}
        defaultValue={value ?? ""}
        aria-invalid={!!error}
        {...rest}
      />
      <Check f={f} error={error} />
    </div>
  );
}

// ClaimReader takes a scan or photo of a claim form, shows it beside the
// claim the model read from it, and saves the claim once a person has
// checked the flagged values.
export function ClaimReader({ payers }: { payers: Payer[] }) {
  const [read, readAction] = useActionState(readClaimForm, {} as ReadState);
  const [saved, saveAction, saving] = useActionState(
    saveReadClaim,
    {} as ReadState,
  );
  const [preview, setPreview] = useState<{ url: string; pdf: boolean } | null>(
    null,
  );
  useEffect(
    () => () => {
      if (preview) URL.revokeObjectURL(preview.url);
    },
    [preview],
  );
  const x = read.extraction;
  const [view, setView] = useState<"model" | "file">("model");
  // The save action is given what was read, so a retry reuses its key.
  const s: ReadState = { ...read, ...saved };

  return (
    <div className="app-read">
      <section className="app-card app-read__doc" aria-labelledby="doc-h">
        <h2 id="doc-h" className="app-card__title">
          The form
        </h2>
        <form action={readAction} className="app-form" noValidate>
          <div className="field">
            <label htmlFor="f-form">Scan or photo of the claim form</label>
            <input
              id="f-form"
              name="form"
              type="file"
              accept="application/pdf,image/png,image/jpeg,image/webp"
              onChange={(e) => {
                const file = e.target.files?.[0];
                setPreview(
                  file
                    ? {
                        url: URL.createObjectURL(file),
                        pdf: file.type === "application/pdf",
                      }
                    : null,
                );
              }}
            />
            <p className="field__hint">
              A PDF, or a JPEG, PNG or WebP photo, up to 10 MB. One claim per
              file.
            </p>
          </div>
          <div className="app-form__foot">
            <Submit className={`app-button${x ? "" : " app-button--primary"}`}>
              {x ? "Read again" : "Read the form"}
            </Submit>
            <Messages state={{ error: read.error }} />
          </div>
        </form>
        {x?.redacted_pages?.length ? (
          <div className="app-read__views">
            <div
              className="app-segment"
              role="group"
              aria-label="Which copy to show"
            >
              <button
                type="button"
                aria-pressed={view === "model"}
                onClick={() => setView("model")}
              >
                What the model saw
              </button>
              <button
                type="button"
                aria-pressed={view === "file"}
                onClick={() => setView("file")}
              >
                Your file
              </button>
            </div>
            {view === "model" && (
              <>
                <p className="app-read__privacy">
                  The patient&rsquo;s name, numbers, date of birth, phone and
                  address were blacked out on Payahub&rsquo;s server before the
                  model saw the form. Sex, age, diagnoses and the bill were left
                  in. The hidden details were read here, and are marked for you
                  to check.
                </p>
                {x.redacted_pages.map((src, i) => (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img
                    key={i}
                    src={src}
                    alt={`Page ${i + 1} as the model saw it`}
                    className="app-read__view"
                  />
                ))}
              </>
            )}
          </div>
        ) : null}
        {preview &&
          (!x?.redacted_pages?.length || view === "file") &&
          (preview.pdf ? (
            <object
              data={preview.url}
              type="application/pdf"
              className="app-read__view"
              aria-label="The claim form"
            >
              <p className="app-muted">
                Your browser cannot show the PDF here; open it beside this page.
              </p>
            </object>
          ) : (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={preview.url}
              alt="The claim form"
              className="app-read__view"
            />
          ))}
      </section>

      <section className="app-card" aria-labelledby="claim-h">
        <h2 id="claim-h" className="app-card__title">
          The claim
        </h2>
        {!x ? (
          <p className="app-empty">
            Choose the form and read it. Each value comes back with how sure the
            reading is; the ones to check are marked, with the words they came
            from. Nothing is saved until you save it.
          </p>
        ) : (
          <form
            className="app-form"
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              const fd = new FormData(e.currentTarget);
              startTransition(() => saveAction(fd));
            }}
          >
            <p
              className={`app-banner${x.review ? "" : " app-banner--good"}`}
              role="status"
            >
              {x.review
                ? `${x.review} ${x.review === 1 ? "value needs" : "values need"} checking against the form. The rest were read clearly.`
                : "Everything was read clearly. Look it over, then save."}
            </p>

            <fieldset className="app-fieldset">
              <legend>Insurer and member</legend>
              <div
                className={`field${x.fields.payer_id?.needs_review || s.fields?.payer_id ? " field--check" : ""}`}
              >
                <label htmlFor="r-payer">Insurer</label>
                <select
                  id="r-payer"
                  name="payer_id"
                  defaultValue={x.claim.payer_id}
                >
                  <option value="">Choose the insurer</option>
                  {payers.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
                <Check f={x.fields.payer_id} error={s.fields?.payer_id} />
              </div>
              <div className="app-form__row">
                <RField
                  label="Member number"
                  name="member_number"
                  path="member.member_number"
                  x={x}
                  s={s}
                  value={x.claim.member.member_number}
                />
                <RField
                  label="Policy number"
                  name="policy_number"
                  path="member.policy_number"
                  x={x}
                  s={s}
                  value={x.claim.member.policy_number}
                />
              </div>
              <div className="app-form__row">
                <RField
                  label="First name"
                  name="first_name"
                  path="member.first_name"
                  x={x}
                  s={s}
                  value={x.claim.member.first_name}
                />
                <RField
                  label="Last name"
                  name="last_name"
                  path="member.last_name"
                  x={x}
                  s={s}
                  value={x.claim.member.last_name}
                />
              </div>
              <div className="app-form__row">
                <RField
                  label="Date of birth"
                  name="birth_date"
                  path="member.birth_date"
                  x={x}
                  s={s}
                  type="date"
                  value={x.claim.member.birth_date}
                />
                <div
                  className={`field${x.fields["member.gender"]?.needs_review ? " field--check" : ""}`}
                >
                  <label htmlFor="r-gender">Gender</label>
                  <select
                    id="r-gender"
                    name="gender"
                    defaultValue={x.claim.member.gender ?? ""}
                  >
                    <option value="">Not given</option>
                    <option value="female">Female</option>
                    <option value="male">Male</option>
                  </select>
                  <Check f={x.fields["member.gender"]} />
                </div>
              </div>
            </fieldset>

            <fieldset className="app-fieldset">
              <legend>Visit</legend>
              <RField
                label="Clinic"
                name="provider_name"
                path="provider.name"
                x={x}
                s={s}
                value={x.claim.provider.name}
              />
              <div className="app-form__row">
                <RField
                  label="Visit date"
                  name="start_date"
                  path="encounter.start_date"
                  x={x}
                  s={s}
                  type="date"
                  value={
                    x.claim.encounter.start_date === "0001-01-01"
                      ? ""
                      : x.claim.encounter.start_date
                  }
                />
                <RField
                  label="Discharge date (admissions)"
                  name="end_date"
                  path="encounter.end_date"
                  x={x}
                  s={s}
                  type="date"
                  value={x.claim.encounter.end_date}
                />
              </div>
              <RField
                label="Authorisation code"
                name="authorization_code"
                path="authorization_code"
                x={x}
                s={s}
                value={x.claim.authorization_code}
              />
            </fieldset>

            <fieldset className="app-fieldset">
              <legend>Diagnoses</legend>
              {x.claim.diagnoses.length === 0 && (
                <p className="app-muted">
                  No diagnosis was read. Add it to the claim after saving.
                </p>
              )}
              {x.claim.diagnoses.map((d, i) => (
                <div key={i} className="app-read__row">
                  <input type="hidden" name="dx" value={i} />
                  <RField
                    label="ICD-10"
                    name={`dx_${i}_code`}
                    path={`diagnoses.${i}.code`}
                    x={x}
                    s={s}
                    value={d.code.value}
                    className="app-read__code"
                  />
                  <RField
                    label="As written"
                    name={`dx_${i}_desc`}
                    path={`diagnoses.${i}.description`}
                    x={x}
                    s={s}
                    value={d.description}
                  />
                  <div className="app-read__opts">
                    <label className="app-choice--plain">
                      <input
                        type="radio"
                        name="dx_primary"
                        value={i}
                        defaultChecked={d.role === "primary"}
                      />{" "}
                      Primary
                    </label>
                    <label className="app-choice--plain">
                      <input type="checkbox" name={`dx_${i}_remove`} /> Leave
                      out
                    </label>
                  </div>
                </div>
              ))}
            </fieldset>

            <fieldset className="app-fieldset">
              <legend>Lines</legend>
              {x.claim.lines.map((l, i) => {
                const matched = x.fields[`lines.${i}.code`];
                return (
                  <div key={i} className="app-read__row app-read__row--line">
                    <input type="hidden" name="line" value={i} />
                    <input
                      type="hidden"
                      name={`line_${i}_code`}
                      value={l.code?.value ?? ""}
                    />
                    <input
                      type="hidden"
                      name={`line_${i}_date`}
                      value={l.service_date ?? ""}
                    />
                    <div className="app-read__item">
                      <RField
                        label="Item"
                        name={`line_${i}_desc`}
                        path={`lines.${i}.description`}
                        x={x}
                        s={s}
                        value={l.description}
                      />
                      <p className="field__hint">
                        {l.code
                          ? `Standard item: ${l.code.display ?? l.code.value}${matched && matched.confidence < 0.9 ? ` (${pct(matched.confidence)} match)` : ""}`
                          : "No standard item matched; your mapping or price list may still find it."}
                      </p>
                    </div>
                    <RField
                      label="Qty"
                      name={`line_${i}_qty`}
                      path={`lines.${i}.quantity`}
                      x={x}
                      s={s}
                      value={l.quantity}
                      inputMode="numeric"
                      className="app-read__qty"
                    />
                    <RField
                      label="Unit price (GHS)"
                      name={`line_${i}_price`}
                      path={`lines.${i}.unit_price`}
                      x={x}
                      s={s}
                      value={
                        l.unit_price ? (l.unit_price / 100).toFixed(2) : ""
                      }
                      inputMode="decimal"
                    />
                    <div className="app-read__opts">
                      <label className="app-choice--plain">
                        <input type="checkbox" name={`line_${i}_remove`} />{" "}
                        Leave out
                      </label>
                    </div>
                  </div>
                );
              })}
            </fieldset>

            <p className="app-muted">
              Read by {x.evidence.model_version} ({x.evidence.prompt_version}).
              Saving makes this a claim like any other: it is checked against
              the insurer&apos;s rules and you send it from its page.
            </p>
            <div className="app-form__foot">
              <button
                type="submit"
                className="app-button app-button--primary"
                disabled={saving}
                aria-busy={saving}
              >
                {saving ? "Saving…" : "Save the claim"}
              </button>
              <Messages state={{ error: saved.error }} />
            </div>
          </form>
        )}
      </section>
    </div>
  );
}
