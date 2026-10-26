import { NextResponse, type NextRequest } from "next/server";

import { api } from "@/lib/api";
import { kindName } from "@/lib/format";
import type { Diagnosis, Item, Payer } from "@/lib/types";

// Type-ahead for the form kit's RemoteSelect. The browser asks this route, the
// route asks the API with the session from the httpOnly cookie, so no token
// reaches the page. Answers are option lists: { data: [{ value, label, description }] }.

type Option = { value: string; label: string; description?: string };

const LIMIT = 20;

async function items(q: string, kind?: string): Promise<Option[] | null> {
  const params = new URLSearchParams({ q });
  if (kind) params.set("kind", kind);
  const res = await api<{ data: Item[] }>(`/v1/items?${params}`);
  if (!res.ok) return null;
  return res.data.data.slice(0, LIMIT).map((it) => ({
    value: it.id,
    // Most names already carry the strength and form ("Amoxicillin 500 mg capsule").
    label: [it.name, it.strength, it.form]
      .filter(
        (p, i) =>
          p && (i === 0 || !it.name.toLowerCase().includes(p.toLowerCase())),
      )
      .join(" "),
    description: `${kindName[it.kind]} · ${it.id}`,
  }));
}

async function diagnoses(q: string): Promise<Option[] | null> {
  const res = await api<{ data: Diagnosis[] }>(
    `/v1/diagnoses?limit=${LIMIT}&q=${encodeURIComponent(q)}`,
  );
  if (!res.ok) return null;
  return res.data.data.map((d) => ({
    value: d.code,
    label: `${d.code} ${d.title}`,
    description: d.billable
      ? undefined
      : "Category: pick a more specific code to bill",
  }));
}

async function payers(q: string): Promise<Option[] | null> {
  const res = await api<{ data: Payer[] }>("/v1/payers");
  if (!res.ok) return null;
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  return res.data.data
    .filter((p) => {
      const hay = `${p.name} ${p.id} ${p.connector.name}`.toLowerCase();
      return words.every((w) => hay.includes(w));
    })
    .slice(0, LIMIT)
    .map((p) => ({
      value: p.id,
      label: p.name,
      description:
        p.kind === "integrated" ? `Through ${p.connector.name}` : "Direct",
    }));
}

export async function GET(
  req: NextRequest,
  ctx: { params: Promise<{ kind: string }> },
) {
  const { kind } = await ctx.params;
  const q = (req.nextUrl.searchParams.get("q") ?? "").trim().slice(0, 120);
  let data: Option[] | null;
  switch (kind) {
    case "items":
      data = await items(q);
      break;
    case "medicines":
      data = await items(q, "medication");
      break;
    case "services":
      data = await items(q, "service");
      break;
    case "diagnoses":
      data = q ? await diagnoses(q) : [];
      break;
    case "payers":
      data = await payers(q);
      break;
    default:
      return NextResponse.json(
        { error: { code: "not_found", message: "Unknown list" } },
        { status: 404 },
      );
  }
  if (data === null) {
    return NextResponse.json(
      { error: { code: "upstream", message: "Search is unavailable" } },
      { status: 502 },
    );
  }
  return NextResponse.json(
    { data },
    { headers: { "Cache-Control": "private, max-age=30" } },
  );
}
