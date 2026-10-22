import { confirmItems, deletePrice } from "@/app/actions";
import { Submit } from "@/components/forms";
import { AddPriceForm, ImportPricesForm } from "@/components/price-forms";
import { api } from "@/lib/api";
import { kindName, money } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { Item, Price } from "@/lib/types";

export const metadata = { title: "Price list" };

const SHOWN = 200;
const SURE = 0.9;
const pick = (p: Price, item: string) => [p.local_code, item, p.name].join("\u001f");

export default async function Prices({ searchParams }: { searchParams: Promise<{ q?: string; show?: string }> }) {
  await requireOrg();
  const { q = "", show = "" } = await searchParams;
  const [res, items] = await Promise.all([
    api<{ data: Price[]; mapped: number }>("/v1/price-list"),
    api<{ data: Item[] }>("/v1/items"),
  ]);
  const all = res.ok ? res.data.data : [];
  const byId = new Map((items.ok ? items.data.data : []).map((it) => [it.id, it]));
  const needle = q.trim().toLowerCase();
  const rows = all.filter(
    (p) =>
      (!needle || p.local_code.toLowerCase().includes(needle) || p.name.toLowerCase().includes(needle)) &&
      (show !== "unmapped" || !p.item_id),
  );
  const unmapped = all.filter((p) => !p.item_id);
  const sure = unmapped.filter((p) => p.suggestion && p.suggestion.confidence >= SURE);

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Price list</h1>
          <p className="app-finding">
            {all.length
              ? `${all.length.toLocaleString("en-GB")} prices. ${unmapped.length ? `${unmapped.length} still need their standard item before claims with them can go to an insurer.` : "Every code is linked to a standard item."}`
              : "Import your price list once. Claim lines then need only your code: the price and name come from here."}
          </p>
        </div>
      </div>

      <div className="app-split">
        <section className="app-card" aria-labelledby="imp-h">
          <h2 id="imp-h" className="app-card__title">
            {all.length ? "Import again" : "Import your price list"}
          </h2>
          <ImportPricesForm empty={!all.length} />
        </section>
        <section className="app-card" aria-labelledby="add-h">
          <h2 id="add-h" className="app-card__title">
            Add one price
          </h2>
          <AddPriceForm />
        </section>
      </div>

      {all.length > 0 && (
        <section className="app-card app-card--flush" aria-labelledby="list-h">
          <div className="app-card__head app-card__head--pad">
            <h2 id="list-h" className="app-card__title">
              Your prices
            </h2>
            <form className="app-inline-form" role="search">
              <div className="field field--inline">
                <label htmlFor="f-q">Search</label>
                <input id="f-q" name="q" defaultValue={q} placeholder="code or name" />
              </div>
              <div className="field field--inline">
                <label htmlFor="f-show">Show</label>
                <select id="f-show" name="show" defaultValue={show}>
                  <option value="">All</option>
                  <option value="unmapped">Without a standard item</option>
                </select>
              </div>
              <button type="submit" className="app-button">
                Show
              </button>
            </form>
          </div>
          {sure.length > 0 && (
            <form action={confirmItems} className="app-banner">
              {sure.map((p) => (
                <input key={p.local_code} type="hidden" name="pick" value={pick(p, p.suggestion!.item.id)} />
              ))}
              <p>
                <strong>{sure.length}</strong> of your codes match a standard item at {SURE * 100}% or better. Check the
                list below, then confirm them together.
              </p>
              <Submit className="app-button">Confirm {sure.length}</Submit>
            </form>
          )}
          <table className="app-table app-prices">
            <thead>
              <tr>
                <th scope="col" className="hide-sm">
                  Your code
                </th>
                <th scope="col">Name</th>
                <th scope="col" className="app-right">
                  Price
                </th>
                <th scope="col">Standard item</th>
                <th scope="col">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.slice(0, SHOWN).map((p) => {
                const it = p.item_id ? byId.get(p.item_id) : undefined;
                const sg = p.suggestion;
                return (
                  <tr key={p.local_code}>
                    <td className="app-id hide-sm">{p.local_code}</td>
                    <td>
                      {p.name}
                      <span className="app-muted app-block">
                        <span className="show-sm-inline">
                          {p.local_code}
                          {p.kind ? " · " : ""}
                        </span>
                        {p.kind ? kindName[p.kind] : ""}
                      </span>
                    </td>
                    <td className="app-right app-nowrap">{money(p.unit_price, p.currency)}</td>
                    <td>
                      {p.item_id ? (
                        <>
                          {it?.name ?? p.item_id}
                          <span className="app-muted app-block">{p.item_id}</span>
                        </>
                      ) : sg ? (
                        <form action={confirmItems} className="app-confirm">
                          <input type="hidden" name="pick" value={pick(p, sg.item.id)} />
                          <span>
                            {sg.item.name}
                            <span className="app-muted app-block">
                              Suggested · {Math.round(sg.confidence * 100)}%
                            </span>
                          </span>
                          <Submit className="app-button app-button--small">Confirm</Submit>
                        </form>
                      ) : (
                        <span className="app-muted">No close standard item</span>
                      )}
                    </td>
                    <td className="app-right">
                      <form action={deletePrice}>
                        <input type="hidden" name="code" value={p.local_code} />
                        <Submit className="app-button app-button--quiet">Remove</Submit>
                      </form>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {rows.length > SHOWN && (
            <p className="app-muted app-pad">
              Showing {SHOWN} of {rows.length.toLocaleString("en-GB")}. Search to find the rest.
            </p>
          )}
          {!rows.length && <p className="app-muted app-pad">Nothing matches.</p>}
        </section>
      )}
    </div>
  );
}
