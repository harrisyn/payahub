import Link from "next/link";

import { deleteMapping } from "@/app/actions";
import { MappingForm, Submit } from "@/components/forms";
import { api } from "@/lib/api";
import { kindName } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { Diagnosis, Item, ItemKind, ItemMapping } from "@/lib/types";

export const metadata = { title: "Services and medicines" };

const kinds: ItemKind[] = ["service", "lab", "imaging", "procedure", "medication", "consumable"];

export default async function Items({ searchParams }: { searchParams: Promise<{ kind?: string; q?: string; dq?: string }> }) {
  await requireOrg();
  const { kind = "", q = "", dq = "" } = await searchParams;
  const query = new URLSearchParams();
  if (kinds.includes(kind as ItemKind)) query.set("kind", kind);
  if (q.trim()) query.set("q", q.trim());
  const [mappings, items, catalogue, diagnoses] = await Promise.all([
    api<{ data: ItemMapping[] }>("/v1/item-mappings"),
    api<{ data: Item[] }>("/v1/items"),
    api<{ data: Item[] }>(`/v1/items?${query}`),
    dq.trim()
      ? api<{ data: Diagnosis[] }>(`/v1/diagnoses?limit=50&q=${encodeURIComponent(dq.trim())}`)
      : Promise.resolve(null),
  ]);
  const found = diagnoses?.ok ? diagnoses.data.data : [];
  const mine = mappings.ok ? mappings.data.data : [];
  const byId = new Map((items.ok ? items.data.data : []).map((it) => [it.id, it]));
  const shown = catalogue.ok ? catalogue.data.data : [];

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Services and medicines</h1>
          <p className="app-finding">
            {mine.length
              ? `${mine.length} of your codes mapped to our standard list. Each insurer's code follows from it.`
              : "Map your own codes to our standard list once. After that, a claim line with your code reaches every insurer with that insurer's code."}
          </p>
        </div>
      </div>

      <section className="app-card" aria-labelledby="map-h">
        <h2 id="map-h" className="app-card__title">
          Map your codes
        </h2>
        <p className="app-muted">
          Paste from your price list or pharmacy system. We suggest the standard item for each line; you confirm.
        </p>
        <MappingForm />
      </section>

      <section className="app-card app-card--flush" aria-labelledby="mine-h">
        <div className="app-card__head app-card__head--pad">
          <h2 id="mine-h" className="app-card__title">
            Your mappings
          </h2>
        </div>
        {mine.length ? (
          <table className="app-table">
            <thead>
              <tr>
                <th scope="col">Your code</th>
                <th scope="col" className="hide-sm">
                  Your name
                </th>
                <th scope="col">Standard item</th>
                <th scope="col">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {mine.map((m) => {
                const it = byId.get(m.item_id);
                return (
                  <tr key={m.local_code}>
                    <td className="app-id">{m.local_code}</td>
                    <td className="hide-sm">{m.local_name || <span className="app-dash">–</span>}</td>
                    <td>
                      {it ? it.name : m.item_id}
                      <span className="app-muted app-block">
                        {it ? kindName[it.kind] : ""} · {m.item_id}
                      </span>
                    </td>
                    <td className="app-right">
                      <form action={deleteMapping}>
                        <input type="hidden" name="code" value={m.local_code} />
                        <Submit className="app-button">Remove</Submit>
                      </form>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        ) : (
          <p className="app-muted app-pad">Nothing mapped yet. Claims can still send standard codes directly.</p>
        )}
      </section>

      <section className="app-card app-card--flush" aria-labelledby="std-h">
        <div className="app-card__head app-card__head--pad">
          <h2 id="std-h" className="app-card__title">
            The standard list
          </h2>
          <form className="app-inline-form" role="search">
            <div className="field field--inline">
              <label htmlFor="f-q">Search</label>
              <input id="f-q" name="q" defaultValue={q} placeholder="paracetamol, FBC…" />
            </div>
            <div className="field field--inline">
              <label htmlFor="f-kind">Kind</label>
              <select id="f-kind" name="kind" defaultValue={kind}>
                <option value="">All</option>
                {kinds.map((k) => (
                  <option key={k} value={k}>
                    {kindName[k]}
                  </option>
                ))}
              </select>
            </div>
            <button type="submit" className="app-button">
              Show
            </button>
          </form>
        </div>
        <table className="app-table">
          <thead>
            <tr>
              <th scope="col">Code</th>
              <th scope="col">Item</th>
              <th scope="col" className="hide-sm">
                Kind
              </th>
              <th scope="col" className="hide-sm">
                Other standards
              </th>
            </tr>
          </thead>
          <tbody>
            {shown.map((it) => (
              <tr key={it.id}>
                <td className="app-id">{it.id}</td>
                <td>{it.name}</td>
                <td className="hide-sm">{kindName[it.kind]}</td>
                <td className="app-muted hide-sm">
                  {it.codes?.map((c) => `${c.system.toUpperCase()} ${c.value}`).join(", ") || <span className="app-dash">–</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {shown.length === 0 && <p className="app-muted app-pad">Nothing matches. Try fewer words.</p>}
      </section>
      <section className="app-card app-card--flush" aria-labelledby="dx-h">
        <div className="app-card__head app-card__head--pad">
          <div>
            <h2 id="dx-h" className="app-card__title">
              Diagnoses (ICD-10)
            </h2>
            <p className="app-muted">Every claim&apos;s diagnosis codes are checked against this list before sending.</p>
          </div>
          <form className="app-inline-form" role="search">
            {kind && <input type="hidden" name="kind" value={kind} />}
            {q && <input type="hidden" name="q" value={q} />}
            <div className="field field--inline">
              <label htmlFor="f-dq">Find a diagnosis</label>
              <input id="f-dq" name="dq" defaultValue={dq} placeholder="malaria, B50, hypertension…" />
            </div>
            <button type="submit" className="app-button">
              Find
            </button>
          </form>
        </div>
        {dq.trim() ? (
          found.length ? (
            <table className="app-table">
              <thead>
                <tr>
                  <th scope="col">Code</th>
                  <th scope="col">Diagnosis</th>
                </tr>
              </thead>
              <tbody>
                {found.map((d) => (
                  <tr key={d.code}>
                    <td className="app-id">{d.code}</td>
                    <td>{d.title}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p className="app-muted app-pad">No ICD-10 code matches “{dq}”. Try a shorter word or the first letters of the code.</p>
          )
        ) : (
          <p className="app-muted app-pad">Search by words or by the start of a code.</p>
        )}
      </section>

      <p className="app-muted app-note">
        The standard list is shared by every clinic and grows as we add insurers. Send a standard code straight from
        your system as <code>{`{"system": "payahub", "value": "MED-0001"}`}</code>, or your own code once it is mapped.{" "}
        <Link href="/docs#items">How it works</Link>
      </p>
    </div>
  );
}
