"use client";

import { useActionState } from "react";

import {
  addPrice,
  importPrices,
  type FormState,
  type ImportState,
} from "@/app/actions";
import { Field, Messages, Submit } from "@/components/forms";
import { Select } from "@/components/ui";
import { kindName } from "@/lib/format";
import type { ItemKind } from "@/lib/types";

const kinds = Object.keys(kindName) as ItemKind[];

export function ImportPricesForm({ empty }: { empty: boolean }) {
  const [s, action] = useActionState(importPrices, {} as ImportState);
  const c = s.changes;
  return (
    <form action={action} className="app-form" noValidate>
      <div className="field">
        <label htmlFor="f-file">Price list file</label>
        <input
          id="f-file"
          name="file"
          type="file"
          accept=".csv,.xlsx,text/csv"
          aria-describedby="f-file-h"
          required
        />
        <p className="field__hint" id="f-file-h">
          CSV or Excel. We look for a code, a name and a price column; a
          category column helps the matching.
        </p>
      </div>
      {!empty && (
        <fieldset className="app-fieldset">
          <legend>Codes already on your list</legend>
          <div className="app-choices">
            <label className="app-choice">
              <input type="radio" name="mode" value="merge" defaultChecked />
              <span>Keep them; add and update from the file</span>
            </label>
            <label className="app-choice">
              <input type="radio" name="mode" value="replace" />
              <span>Remove any the file does not have</span>
            </label>
          </div>
        </fieldset>
      )}
      <div className="app-form__foot">
        <Submit className="app-button app-button--primary">Import</Submit>
        <Messages state={s} />
      </div>
      {c && (
        <p className="app-muted" role="status">
          {c.added} added, {c.changed} changed, {c.unchanged} unchanged
          {c.removed ? `, ${c.removed} removed` : ""}.
        </p>
      )}
      {!!s.skipped?.length && (
        <details className="app-details" open={s.skipped.length <= 5}>
          <summary>
            {s.skipped.length} {s.skipped.length === 1 ? "row" : "rows"} not
            imported
          </summary>
          <ul className="app-list">
            {s.skipped.slice(0, 100).map((r) => (
              <li key={r.line}>
                Row {r.line}: {r.reason}
              </li>
            ))}
          </ul>
        </details>
      )}
    </form>
  );
}

export function AddPriceForm() {
  const [s, action] = useActionState(addPrice, {} as FormState);
  return (
    <form action={action} className="app-form" noValidate>
      <div className="app-form__row">
        <Field label="Your code" name="code" required />
        <Field label="Name" name="name" required />
      </div>
      <div className="app-form__row">
        <Select
          id="f-kind"
          name="kind"
          label="Kind"
          defaultValue=""
          data={[
            { value: "", label: "Not sure" },
            ...kinds.map((k) => ({ value: k, label: kindName[k] })),
          ]}
        />
        <Field
          label="Price (GHS)"
          name="price"
          inputMode="decimal"
          placeholder="45.00"
          required
        />
      </div>
      <div className="app-form__foot">
        <Submit className="app-button">Add to the list</Submit>
        <Messages state={s} />
      </div>
    </form>
  );
}
