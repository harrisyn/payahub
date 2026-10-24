"use client";

import { useActionState, useState } from "react";

import {
  acceptSuggestions,
  askModelForCodes,
  importPayers,
  loadPayerList,
  saveCodes,
  savePayer,
  savePayerRules,
  type CodesState,
  type FormState,
  type ListState,
  type PayerImportRow,
  type PayerImportState,
} from "@/app/actions";
import { Field, Messages, Submit } from "@/components/forms";
import { Select, Textarea, TextInput } from "@/components/ui";
import { kindName } from "@/lib/format";
import type { Connector, ItemCode, OpPayer, Policy } from "@/lib/types";

// PayerForm adds a payer to the shared catalogue or edits one. The settings
// it asks for are the chosen connector's payer fields, so they change with it.
export function PayerForm({
  payer,
  connectors,
}: {
  payer?: OpPayer;
  connectors: Connector[];
}) {
  const [s, action] = useActionState(savePayer, {} as FormState);
  const [conn, setConn] = useState(payer?.connector.id ?? "");
  const c = connectors.find((x) => x.id === conn);
  const err = (k: string) => s.fields?.[k];
  return (
    <form action={action} className="app-form" noValidate>
      {payer ? (
        <input type="hidden" name="id" value={payer.id} />
      ) : (
        <input type="hidden" name="new" value="1" />
      )}
      <div className="app-form__row">
        <Field
          label="Name"
          name="name"
          defaultValue={payer?.name}
          error={err("name")}
          required
        />
        {!payer && (
          <Field
            label="Id"
            name="id"
            error={err("id")}
            hint="Lowercase, fixed once saved. Claims name the payer by it."
            placeholder="acacia_health"
            required
          />
        )}
        <Field
          label="Country"
          name="country"
          defaultValue={payer?.country ?? "GH"}
          error={err("country")}
          maxLength={2}
          required
        />
      </div>
      <Select
        id="f-connector"
        name="connector"
        label="Reached through"
        required
        placeholder="Choose a platform or direct connector"
        value={conn || null}
        onChange={(v) => setConn(v ?? "")}
        error={err("connector")}
        data={connectors.map((x) => ({
          value: x.id,
          label: x.name,
          description: `${x.kind === "platform" ? "Platform" : "Direct"}${x.status === "porting" ? ", not yet ported" : ""}`,
        }))}
      />
      {c && c.payer_fields.length > 0 && (
        <fieldset className="app-fieldset">
          <legend>The payer&apos;s settings on {c.name}</legend>
          <div className="app-form__row">
            {c.payer_fields.map((f) => (
              <Field
                key={`${c.id}-${f.name}`}
                label={f.label + (f.required ? "" : " (optional)")}
                name={`field_${f.name}`}
                defaultValue={
                  payer?.connector.id === c.id ? payer.fields[f.name] : ""
                }
                error={err(`fields.${f.name}`)}
              />
            ))}
          </div>
        </fieldset>
      )}
      {c && c.payer_fields.length === 0 && (
        <p className="app-muted">{c.name} needs no settings for each payer.</p>
      )}
      <Textarea
        id="f-notes"
        name="notes"
        label="Notes for organisations"
        minRows={2}
        defaultValue={payer?.notes}
      />
      {payer && (
        <label className="app-switchrow">
          <input
            type="checkbox"
            name="inactive"
            defaultChecked={payer.inactive}
          />
          <span>
            <span className="app-step__title">Withdraw from the catalogue</span>
            <span className="app-muted">
              No new claims; claims already sent keep their status updates.
            </span>
          </span>
        </label>
      )}
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">
          {payer ? "Save payer" : "Add payer"}
        </Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

// ListForm loads the payer's own code list (its tariff) from a CSV file.
export function ListForm({ payer, size }: { payer: string; size: number }) {
  const [s, action] = useActionState(loadPayerList, {} as ListState);
  return (
    <form action={action} className="app-form" noValidate>
      <input type="hidden" name="payer" value={payer} />
      <div className="field">
        <label htmlFor="f-file">
          {size ? "Replace the list" : "Load the list"}
        </label>
        <input
          id="f-file"
          name="file"
          type="file"
          accept=".csv,text/csv"
          aria-describedby="f-file-h"
        />
        <p className="field__hint" id="f-file-h">
          CSV with a header row: code and name, then group and price if the
          payer gives them. The payer&rsquo;s rules for a code go in max
          quantity, sex (F or M), min age, max age and authorization (yes)
          columns; blank means no limit.
        </p>
      </div>
      <details className="app-details">
        <summary>Or paste it</summary>
        <Textarea
          name="csv"
          aria-label="The list as CSV"
          minRows={6}
          placeholder={
            "code,name,group,price,max quantity,sex,min age,max age,authorization\nCON-001,General consultation,Consultation,120.00,,,,,\nANC-01,Antenatal visit,Consultation,60.00,,F,12,55,"
          }
        />
      </details>
      <div className="app-form__foot">
        <Submit className="app-button">Load list</Submit>
        <Messages state={s} />
      </div>
      {!!s.skipped?.length && (
        <details className="app-details" open={s.skipped.length <= 5}>
          <summary>
            {s.skipped.length} {s.skipped.length === 1 ? "row" : "rows"} skipped
          </summary>
          <ul className="app-list">
            {s.skipped.slice(0, 50).map((r) => (
              <li key={r.line}>
                Line {r.line}: {r.reason}
              </li>
            ))}
          </ul>
        </details>
      )}
    </form>
  );
}

// RulesForm sets how strictly the payer's list applies: whether a price above
// its tariff, or a code outside its list, stops a claim or only warns.
export function RulesForm({
  payer,
  rules,
}: {
  payer: string;
  rules: { price_policy: Policy; unlisted_policy: Policy };
}) {
  const [s, action] = useActionState(savePayerRules, {} as FormState);
  const options = [
    { value: "warn", label: "Send it, with a warning" },
    { value: "block", label: "Stop the claim" },
  ];
  return (
    <form action={action} className="app-form">
      <input type="hidden" name="payer" value={payer} />
      <Select
        id="f-price-policy"
        name="price_policy"
        label="A line priced above the payer's tariff"
        defaultValue={rules.price_policy}
        data={options}
      />
      <Select
        id="f-unlisted-policy"
        name="unlisted_policy"
        label="A code that is not in the payer's list"
        defaultValue={rules.unlisted_policy}
        data={options}
      />
      <div className="app-form__foot">
        <Submit className="app-button">Save rules</Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}

export function AcceptForm({
  payer,
  waiting,
  source = "words",
}: {
  payer: string;
  waiting: number;
  source?: "words" | "model";
}) {
  const [s, action] = useActionState(acceptSuggestions, {} as FormState);
  const what = source === "model" ? "model pick" : "match";
  return (
    <form action={action} className="app-inline-form">
      <input type="hidden" name="payer" value={payer} />
      <input type="hidden" name="source" value={source} />
      <input type="hidden" name="min_confidence" value="0.9" />
      {waiting > 0 ? (
        <Submit className="app-button">
          Accept {waiting} sure{" "}
          {waiting === 1
            ? what
            : source === "model"
              ? "model picks"
              : "matches"}
        </Submit>
      ) : source === "words" ? (
        <span className="app-muted">No sure matches left to accept.</span>
      ) : null}
      <Messages state={s} />
    </form>
  );
}

// ModelCodesForm asks the code-matching model for the payer's code of every
// item without one. It can take a few minutes on a long list.
export function ModelCodesForm({
  payer,
  missing,
}: {
  payer: string;
  missing: number;
}) {
  const [s, action] = useActionState(askModelForCodes, {} as FormState);
  return (
    <form action={action} className="app-inline-form">
      <input type="hidden" name="payer" value={payer} />
      <Submit
        className="app-button"
        pending="The model is reading the list…"
        disabled={!missing}
      >
        Ask the model ({missing} without a code)
      </Submit>
      <Messages state={s} />
    </form>
  );
}

const pct = (n: number) => `${Math.round(n * 100)}%`;

// CodesTable is every standard item with the payer's code for it. Codes are
// edited in place; a suggestion from the payer's list fills the field, and
// nothing is saved until the operator saves.
export function CodesTable({
  payer,
  rows,
  hasList,
}: {
  payer: string;
  rows: ItemCode[];
  hasList: boolean;
}) {
  const [s, action] = useActionState(saveCodes, {} as CodesState);
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(rows.map((r) => [r.item.id, r.code?.code ?? ""])),
  );
  const changed = rows.filter(
    (r) => (values[r.item.id] ?? "") !== (r.code?.code ?? ""),
  ).length;
  return (
    <form action={action} noValidate>
      <input type="hidden" name="payer" value={payer} />
      <table className="app-table app-codes">
        <thead>
          <tr>
            <th scope="col">Standard item</th>
            <th scope="col">Payer&apos;s code</th>
            <th scope="col">
              {hasList ? "From the payer's list" : "Description"}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const id = r.item.id;
            const v = values[id] ?? "";
            const dirty = v !== (r.code?.code ?? "");
            const fieldErr = s.fields?.[id];
            return (
              <tr key={id} className={dirty ? "app-row--dirty" : undefined}>
                <td>
                  {r.item.name}
                  <span className="app-muted app-block">
                    {kindName[r.item.kind]} · {id}
                  </span>
                </td>
                <td className="app-codecell">
                  <input
                    type="hidden"
                    name={`was:${id}`}
                    value={r.code?.code ?? ""}
                  />
                  <TextInput
                    size="xs"
                    className="app-codeinput"
                    name={`code:${id}`}
                    value={v}
                    onChange={(e) =>
                      setValues({ ...values, [id]: e.target.value })
                    }
                    aria-label={`${r.item.name}: payer's code`}
                    error={fieldErr}
                    placeholder="No code"
                    spellCheck={false}
                  />
                </td>
                <td>
                  {r.code && !dirty ? (
                    <>
                      {r.code.description || (
                        <span className="app-dash">–</span>
                      )}
                      {r.generated && (
                        <span className="app-tag">test codes</span>
                      )}
                      {r.in_list === false && (
                        <span className="app-pill app-pill--wait app-gap">
                          not in list
                        </span>
                      )}
                    </>
                  ) : r.suggestions.length || r.model_pick ? (
                    <span className="app-sugs">
                      {r.model_pick &&
                        (r.model_pick.code ? (
                          <button
                            type="button"
                            className="app-sug app-sug--model"
                            aria-pressed={v === r.model_pick.code}
                            onClick={() =>
                              setValues({ ...values, [id]: r.model_pick!.code })
                            }
                            title={`${r.model_pick.model}: ${r.model_pick.reason}`}
                          >
                            <span className="app-tag">model</span>
                            <span className="app-id">
                              {r.model_pick.code}
                            </span>{" "}
                            {r.model_pick.name}
                            <span className="app-sug__conf">
                              {pct(r.model_pick.confidence)}
                            </span>
                          </button>
                        ) : (
                          <span className="app-muted app-block">
                            The model found no code for this in the list.
                          </span>
                        ))}
                      {r.suggestions
                        .filter((g) => g.code !== r.model_pick?.code)
                        .map((g) => (
                          <button
                            type="button"
                            key={g.code}
                            className="app-sug"
                            aria-pressed={v === g.code}
                            onClick={() =>
                              setValues({ ...values, [id]: g.code })
                            }
                            title={`Use ${g.code}`}
                          >
                            <span className="app-id">{g.code}</span> {g.name}
                            <span className="app-sug__conf">
                              {pct(g.confidence)}
                            </span>
                          </button>
                        ))}
                    </span>
                  ) : (
                    <span className="app-muted">
                      {hasList ? "No close match in the list" : "–"}
                    </span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <div className="app-savebar">
        <Submit className="app-button app-button--primary">
          {changed
            ? `Save ${changed} ${changed === 1 ? "change" : "changes"}`
            : "Save codes"}
        </Submit>
        <Messages state={s} />
        {!!s.warnings?.length && (
          <ul className="app-list app-warn">
            {s.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        )}
      </div>
    </form>
  );
}

const actionLabel: Record<PayerImportRow["action"], [string, string]> = {
  add: ["Add", "app-pill--good"],
  fill: ["Fill setting", "app-pill--good"],
  same: ["No change", "app-pill--neutral"],
  conflict: ["Conflict", "app-pill--bad"],
  skip: ["Skip", "app-pill--wait"],
};

// ImportPayersForm loads payers exported from Serenity: check the file,
// read what each row will do, then import it.
export function ImportPayersForm({
  platforms,
}: {
  platforms: Record<string, string>;
}) {
  const [s, action] = useActionState(importPayers, {} as PayerImportState);
  const rows = s.rows ?? [];
  const count = (a: PayerImportRow["action"]) =>
    rows.filter((r) => r.action === a).length;
  const changes = count("add") + count("fill");
  return (
    <div className="app-stack">
      <form action={action} className="app-inline-form" noValidate>
        <div className="field field--inline">
          <label htmlFor="f-upload">
            Export of Serenity&apos;s payers (CSV or Excel)
          </label>
          <input
            id="f-upload"
            name="upload"
            type="file"
            accept=".csv,.xlsx,text/csv"
          />
        </div>
        <input type="hidden" name="intent" value="check" />
        <Submit className="app-button">Check the file</Submit>
      </form>
      <Messages state={s} />
      {rows.length > 0 && (
        <>
          <p className="app-muted">
            {count("add")} to add, {count("fill")} to fill, {count("same")}{" "}
            unchanged, {count("conflict")} in conflict, {count("skip")} skipped.
          </p>
          <table className="app-table">
            <thead>
              <tr>
                <th scope="col">Row</th>
                <th scope="col">Payer</th>
                <th scope="col">Platform and reference</th>
                <th scope="col">What happens</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.line}>
                  <td className="app-tnum">{r.line}</td>
                  <td>
                    {r.name}
                    {r.id && (
                      <span className="app-muted app-block">{r.id}</span>
                    )}
                  </td>
                  <td>
                    {r.connector ? (
                      (platforms[r.connector] ?? r.connector)
                    ) : (
                      <span className="app-dash">–</span>
                    )}
                    {r.fields && Object.keys(r.fields).length > 0 && (
                      <span className="app-muted app-block">
                        {Object.values(r.fields).join(", ")}
                      </span>
                    )}
                  </td>
                  <td>
                    <span className={`app-pill ${actionLabel[r.action][1]}`}>
                      {actionLabel[r.action][0]}
                    </span>
                    {r.reason && (
                      <span className="app-muted app-block">{r.reason}</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {s.file && changes > 0 && (
            <form action={action} className="app-form__foot">
              <input type="hidden" name="file" value={s.file} />
              <input type="hidden" name="intent" value="import" />
              <Submit className="app-button app-button--primary">
                Import {changes} {changes === 1 ? "change" : "changes"}
              </Submit>
            </form>
          )}
        </>
      )}
    </div>
  );
}
