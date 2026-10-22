"use client";

import { useActionState, useState } from "react";

import {
  acceptSuggestions,
  loadPayerList,
  saveCodes,
  savePayer,
  type CodesState,
  type FormState,
  type ListState,
} from "@/app/actions";
import { Field, Messages, Submit } from "@/components/forms";
import { kindName } from "@/lib/format";
import type { Connector, ItemCode, OpPayer } from "@/lib/types";

// PayerForm adds a payer to the shared catalogue or edits one. The settings
// it asks for are the chosen connector's payer fields, so they change with it.
export function PayerForm({ payer, connectors }: { payer?: OpPayer; connectors: Connector[] }) {
  const [s, action] = useActionState(savePayer, {} as FormState);
  const [conn, setConn] = useState(payer?.connector.id ?? "");
  const c = connectors.find((x) => x.id === conn);
  const err = (k: string) => s.fields?.[k];
  return (
    <form action={action} className="app-form" noValidate>
      {payer ? <input type="hidden" name="id" value={payer.id} /> : <input type="hidden" name="new" value="1" />}
      <div className="app-form__row">
        <Field label="Name" name="name" defaultValue={payer?.name} error={err("name")} required />
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
        <Field label="Country" name="country" defaultValue={payer?.country ?? "GH"} error={err("country")} maxLength={2} required />
      </div>
      <div className="field">
        <label htmlFor="f-connector">Reached through</label>
        <select
          id="f-connector"
          name="connector"
          value={conn}
          onChange={(e) => setConn(e.target.value)}
          aria-invalid={!!err("connector")}
          required
        >
          <option value="" disabled>
            Choose a platform or direct connector
          </option>
          {connectors.map((x) => (
            <option key={x.id} value={x.id}>
              {x.name} ({x.kind === "platform" ? "platform" : "direct"}
              {x.status === "porting" ? ", not yet ported" : ""})
            </option>
          ))}
        </select>
        {err("connector") && <p className="field__error">{err("connector")}</p>}
      </div>
      {c && c.payer_fields.length > 0 && (
        <fieldset className="app-fieldset">
          <legend>The payer&apos;s settings on {c.name}</legend>
          <div className="app-form__row">
            {c.payer_fields.map((f) => (
              <Field
                key={`${c.id}-${f.name}`}
                label={f.label + (f.required ? "" : " (optional)")}
                name={`field_${f.name}`}
                defaultValue={payer?.connector.id === c.id ? payer.fields[f.name] : ""}
                error={err(`fields.${f.name}`)}
              />
            ))}
          </div>
        </fieldset>
      )}
      {c && c.payer_fields.length === 0 && <p className="app-muted">{c.name} needs no settings for each payer.</p>}
      <div className="field">
        <label htmlFor="f-notes">Notes for organisations</label>
        <textarea id="f-notes" name="notes" className="app-textarea app-textarea--short" defaultValue={payer?.notes} />
      </div>
      {payer && (
        <label className="app-switchrow">
          <input type="checkbox" name="inactive" defaultChecked={payer.inactive} />
          <span>
            <span className="app-step__title">Withdraw from the catalogue</span>
            <span className="app-muted">No new claims; claims already sent keep their status updates.</span>
          </span>
        </label>
      )}
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">{payer ? "Save payer" : "Add payer"}</Submit>
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
        <label htmlFor="f-file">{size ? "Replace the list" : "Load the list"}</label>
        <input id="f-file" name="file" type="file" accept=".csv,text/csv" aria-describedby="f-file-h" />
        <p className="field__hint" id="f-file-h">
          CSV with a header row: code and name, then group and price if the payer gives them.
        </p>
      </div>
      <details className="app-details">
        <summary>Or paste it</summary>
        <textarea name="csv" className="app-textarea" placeholder={"code,name,group,price\nCON-001,General consultation,Consultation,120.00"} />
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

export function AcceptForm({ payer, waiting }: { payer: string; waiting: number }) {
  const [s, action] = useActionState(acceptSuggestions, {} as FormState);
  return (
    <form action={action} className="app-inline-form">
      <input type="hidden" name="payer" value={payer} />
      <input type="hidden" name="min_confidence" value="0.9" />
      {waiting > 0 ? (
        <Submit className="app-button">
          Accept {waiting} sure {waiting === 1 ? "match" : "matches"}
        </Submit>
      ) : (
        <span className="app-muted">No sure matches left to accept.</span>
      )}
      <Messages state={s} />
    </form>
  );
}

const pct = (n: number) => `${Math.round(n * 100)}%`;

// CodesTable is every standard item with the payer's code for it. Codes are
// edited in place; a suggestion from the payer's list fills the field, and
// nothing is saved until the operator saves.
export function CodesTable({ payer, rows, hasList }: { payer: string; rows: ItemCode[]; hasList: boolean }) {
  const [s, action] = useActionState(saveCodes, {} as CodesState);
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(rows.map((r) => [r.item.id, r.code?.code ?? ""])),
  );
  const changed = rows.filter((r) => (values[r.item.id] ?? "") !== (r.code?.code ?? "")).length;
  return (
    <form action={action} noValidate>
      <input type="hidden" name="payer" value={payer} />
      <table className="app-table app-codes">
        <thead>
          <tr>
            <th scope="col">Standard item</th>
            <th scope="col">Payer&apos;s code</th>
            <th scope="col">{hasList ? "From the payer's list" : "Description"}
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
                  <input type="hidden" name={`was:${id}`} value={r.code?.code ?? ""} />
                  <input
                    className="app-codeinput"
                    name={`code:${id}`}
                    value={v}
                    onChange={(e) => setValues({ ...values, [id]: e.target.value })}
                    aria-label={`${r.item.name}: payer's code`}
                    aria-invalid={!!fieldErr}
                    placeholder="No code"
                    spellCheck={false}
                  />
                  {fieldErr && <span className="field__error app-block">{fieldErr}</span>}
                </td>
                <td>
                  {r.code && !dirty ? (
                    <>
                      {r.code.description || <span className="app-dash">–</span>}
                      {r.generated && <span className="app-tag">test codes</span>}
                      {r.in_list === false && <span className="app-pill app-pill--wait app-gap">not in list</span>}
                    </>
                  ) : r.suggestions.length ? (
                    <span className="app-sugs">
                      {r.suggestions.map((g) => (
                        <button
                          type="button"
                          key={g.code}
                          className="app-sug"
                          aria-pressed={v === g.code}
                          onClick={() => setValues({ ...values, [id]: g.code })}
                          title={`Use ${g.code}`}
                        >
                          <span className="app-id">{g.code}</span> {g.name}
                          <span className="app-sug__conf">{pct(g.confidence)}</span>
                        </button>
                      ))}
                    </span>
                  ) : (
                    <span className="app-muted">{hasList ? "No close match in the list" : "–"}</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <div className="app-savebar">
        <Submit className="app-button app-button--primary">
          {changed ? `Save ${changed} ${changed === 1 ? "change" : "changes"}` : "Save codes"}
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
