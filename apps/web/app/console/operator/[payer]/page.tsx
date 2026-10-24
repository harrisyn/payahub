import Link from "next/link";
import { notFound } from "next/navigation";

import {
  AcceptForm,
  CodesTable,
  ListForm,
  PayerForm,
  RulesForm,
} from "@/components/operator-forms";
import { api } from "@/lib/api";
import { dateTime, fieldLabels, kindName, money } from "@/lib/format";
import { requireOperator } from "@/lib/session";
import type {
  CatalogChange,
  Connector,
  ItemCode,
  ItemKind,
  ListEntry,
  OpPayer,
  PayerCodes,
} from "@/lib/types";
import { Select } from "@/components/ui";

export const metadata = { title: "Payer" };

const kinds: ItemKind[] = [
  "service",
  "lab",
  "imaging",
  "procedure",
  "medication",
  "consumable",
];

// sure mirrors the API's rule for accepting a match without a person picking
// it: 90% or better and 10 points ahead of the next.
const sure = (r: ItemCode) => {
  const [a, b] = r.suggestions;
  return (
    !r.code &&
    !!a &&
    a.confidence >= 0.9 &&
    (!b || a.confidence - b.confidence >= 0.1)
  );
};

function describe(c: CatalogChange): string {
  const d = c.detail;
  switch (c.kind) {
    case "payer_created":
      return "Added to the catalogue";
    case "payer_updated":
      return d.inactive ? "Settings saved; withdrawn" : "Settings saved";
    case "codes_changed": {
      const set = Number(d.set ?? 0);
      const removed = Number(d.removed ?? 0);
      return [
        set && `${set} ${set === 1 ? "code" : "codes"} set`,
        removed && `${removed} removed`,
      ]
        .filter(Boolean)
        .join(", ");
    }
    case "list_loaded": {
      const n = Number(d.with_rules ?? 0);
      return `Code list loaded: ${Number(d.entries ?? 0).toLocaleString("en-GB")} codes${n ? `, ${n} with rules` : ""}`;
    }
    case "rules_changed":
      return `Rules: prices above the tariff ${d.price_policy === "block" ? "stop a claim" : "warn"}; codes outside the list ${d.unlisted_policy === "block" ? "stop a claim" : "warn"}`;
    default:
      return "Seeded from the shipped catalogue";
  }
}

export default async function OperatorPayer({
  params,
  searchParams,
}: {
  params: Promise<{ payer: string }>;
  searchParams: Promise<{ kind?: string; show?: string }>;
}) {
  await requireOperator();
  const { payer: id } = await params;
  const { kind = "", show = "" } = await searchParams;
  const enc = encodeURIComponent(id);
  const [payers, connectors, codes, changes, limited] = await Promise.all([
    api<{ data: OpPayer[] }>("/v1/operator/payers"),
    api<{ data: Connector[] }>("/v1/connectors"),
    api<PayerCodes>(`/v1/operator/payers/${enc}/codes`),
    api<{ data: CatalogChange[] }>(`/v1/operator/payers/${enc}/changes`),
    api<{ data: ListEntry[]; total: number }>(
      `/v1/operator/payers/${enc}/list?limited=true`,
    ),
  ]);
  const ruled = limited.ok ? limited.data : { data: [], total: 0 };
  const p = payers.ok ? payers.data.data.find((x) => x.id === id) : undefined;
  if (!p || !codes.ok) notFound();
  const label = fieldLabels(connectors.ok ? connectors.data.data : []);
  const missing = p.missing_fields.map((f) => label(p.connector.id, f));
  const all = [...codes.data.data].sort(
    (a, b) => kinds.indexOf(a.item.kind) - kinds.indexOf(b.item.kind),
  );
  const hasList = codes.data.list_size > 0;
  const waiting = all.filter(sure).length;
  const notInList = all.filter((r) => r.in_list === false).length;
  const rows = all.filter(
    (r) =>
      (!kind || r.item.kind === kind) &&
      (show !== "missing" || !r.code) &&
      (show !== "outside" || r.in_list === false),
  );
  const q = (k: string, s: string) => {
    const u = new URLSearchParams();
    if (k) u.set("kind", k);
    if (s) u.set("show", s);
    const t = u.toString();
    return t ? `?${t}` : "?";
  };
  const generated = all.some((r) => r.generated);

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <p className="app-crumb">
            <Link href="/console/operator">Catalogue</Link>
          </p>
          <h1>{p.name}</h1>
          <p className="app-finding">
            {p.kind === "integrated"
              ? `Reached through ${p.connector.name}. `
              : `Reached directly through ${p.connector.name}. `}
            {p.inactive
              ? "Withdrawn: no organisation can choose it."
              : missing.length
                ? `Not taking claims until its ${missing.join(" and ")} ${missing.length === 1 ? "is" : "are"} set below.`
                : p.status === "available"
                  ? "Taking claims."
                  : `Waits for the ${p.connector.name} connector.`}
          </p>
        </div>
      </div>

      <dl className="app-strip app-card">
        <div>
          <dt>Standard items with a code</dt>
          <dd>
            {p.coded_items} <span className="app-unit">of {p.total_items}</span>
          </dd>
        </div>
        <div>
          <dt>Payer&apos;s own list</dt>
          <dd>
            {codes.data.list_size.toLocaleString("en-GB")}{" "}
            <span className="app-unit">codes</span>
          </dd>
        </div>
        <div>
          <dt>Sure matches waiting</dt>
          <dd>{hasList ? waiting : <span className="app-dash">–</span>}</dd>
        </div>
      </dl>

      <section className="app-card app-card--flush" aria-labelledby="codes-h">
        <div className="app-card__head app-card__head--pad">
          <div>
            <h2 id="codes-h" className="app-card__title">
              Codes
            </h2>
            <p className="app-muted">
              {generated
                ? "A test payer: every item has a made-up code. Real payers' codes are set here."
                : hasList
                  ? `Pick from ${p.name}'s list, or type a code. ${notInList ? `${notInList} saved ${notInList === 1 ? "code is" : "codes are"} not in the list.` : ""}`
                  : `Type ${p.name}'s code for each item, or load its list first to get matches.`}
            </p>
          </div>
          {hasList && <AcceptForm payer={p.id} waiting={waiting} />}
        </div>
        <nav className="app-tabs" aria-label="Kind">
          <Link href={q("", show)} aria-current={!kind ? "page" : undefined}>
            All
          </Link>
          {kinds.map((k) => {
            const c = codes.data.coverage[k];
            return c ? (
              <Link
                key={k}
                href={q(k, show)}
                aria-current={kind === k ? "page" : undefined}
              >
                {kindName[k]}{" "}
                <span className="app-muted num">
                  {c.coded}/{c.total}
                </span>
              </Link>
            ) : null;
          })}
        </nav>
        <form className="app-inline-form app-pad-y">
          {kind && <input type="hidden" name="kind" value={kind} />}
          <Select
            id="f-show"
            name="show"
            label="Show"
            defaultValue={show}
            data={[
              { value: "", label: "Every item" },
              { value: "missing", label: "Items without a code" },
              ...(hasList
                ? [{ value: "outside", label: "Codes not in the payer's list" }]
                : []),
            ]}
            className="app-inline-form__show"
          />
          <button type="submit" className="app-button">
            Show
          </button>
        </form>
        {rows.length ? (
          <CodesTable
            key={`${kind}-${show}`}
            payer={p.id}
            rows={rows}
            hasList={hasList}
          />
        ) : (
          <p className="app-muted app-pad">Nothing to show here.</p>
        )}
      </section>

      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="set-h">
          <h2 id="set-h" className="app-card__title">
            Payer settings
          </h2>
          <PayerForm
            payer={p}
            connectors={connectors.ok ? connectors.data.data : []}
          />
        </section>
        <div className="app-stack">
          <section className="app-card" aria-labelledby="list-h">
            <h2 id="list-h" className="app-card__title">
              {p.name}&apos;s code list
            </h2>
            <p className="app-muted">
              {hasList
                ? `${codes.data.list_size.toLocaleString("en-GB")} codes loaded. Matches and checks use it.`
                : "Load the payer's tariff to match its codes to the standard items and check every code against it."}
            </p>
            <ListForm payer={p.id} size={codes.data.list_size} />
          </section>
          <section className="app-card" aria-labelledby="rules-h">
            <h2 id="rules-h" className="app-card__title">
              Rules
            </h2>
            <p className="app-muted">
              {hasList
                ? "Claims to this payer are checked against its list before they go. What the list says about a code (most per claim, one sex, an age range, authorisation) always stops a claim that breaks it."
                : "Load the payer's list first: the rules apply to the codes in it."}
            </p>
            <RulesForm payer={p.id} rules={p.rules} />
          </section>
          <section className="app-card" aria-labelledby="hist-h">
            <h2 id="hist-h" className="app-card__title">
              Changes
            </h2>
            {changes.ok && changes.data.data.length ? (
              <ol className="app-trail">
                {changes.data.data.map((c, i) => (
                  <li key={i} className="app-trail__item">
                    <span className="app-trail__when">
                      {dateTime(c.changed_at)}
                    </span>
                    <span>
                      <span className="app-step__title">{describe(c)}</span>
                      <span className="app-muted app-block">
                        by {c.changed_by}
                      </span>
                    </span>
                  </li>
                ))}
              </ol>
            ) : (
              <p className="app-muted">No changes since it was seeded.</p>
            )}
          </section>
        </div>
      </div>

      {ruled.total > 0 && (
        <section className="app-card app-card--flush" aria-labelledby="ruled-h">
          <div className="app-card__head app-card__head--pad">
            <h2 id="ruled-h" className="app-card__title">
              Codes with rules
            </h2>
            <p className="app-muted">
              {ruled.total.toLocaleString("en-GB")} of the payer&apos;s codes
              carry a limit beyond their price.
            </p>
          </div>
          <table className="app-table app-table--stack">
            <thead>
              <tr>
                <th scope="col">Code</th>
                <th scope="col">Name</th>
                <th scope="col" className="app-right">
                  Tariff
                </th>
                <th scope="col">Rules</th>
              </tr>
            </thead>
            <tbody>
              {ruled.data.slice(0, 100).map((e) => (
                <tr key={e.code}>
                  <td className="app-id">{e.code}</td>
                  <td>{e.name}</td>
                  <td className="app-right app-id">
                    {e.price != null ? (
                      money(e.price)
                    ) : (
                      <span className="app-dash">–</span>
                    )}
                  </td>
                  <td>{rulesText(e)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </div>
  );
}

function rulesText(e: ListEntry) {
  const age =
    e.min_age != null && e.max_age != null
      ? `aged ${e.min_age} to ${e.max_age}`
      : e.min_age != null
        ? `aged ${e.min_age} and over`
        : e.max_age != null
          ? `aged up to ${e.max_age}`
          : null;
  return [
    e.max_quantity != null && `at most ${e.max_quantity} per claim`,
    e.sex && (e.sex === "female" ? "women only" : "men only"),
    age,
    e.needs_authorization && "needs authorisation",
  ]
    .filter(Boolean)
    .join(", ");
}
