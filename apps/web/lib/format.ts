import type { ClaimState, Item, ItemKind, Method } from "./types";

export const money = (minor: number, currency = "GHS") =>
  `${currency} ${(minor / 100).toLocaleString("en-GH", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;

export const date = (iso: string) =>
  new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric", timeZone: "Africa/Accra" });

export const dateTime = (iso: string) =>
  new Date(iso).toLocaleString("en-GB", {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    timeZone: "Africa/Accra",
  });

export const daysLeft = (iso: string | null) =>
  iso ? Math.max(0, Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000)) : 0;

export const methodName: Record<Method, string> = {
  rest: "REST API",
  batch: "Spreadsheet upload",
  documents: "Scanned forms and PDFs",
  web_form: "Web form",
};

export const stateLabel: Record<ClaimState, string> = {
  draft: "Draft",
  mapped: "Mapped",
  validated: "Checked",
  needs_review: "Needs review",
  ready: "Ready",
  submitted: "Sent",
  acknowledged: "Acknowledged",
  approved: "Approved",
  rejected: "Rejected",
  paid: "Paid",
  withdrawn: "Withdrawn",
};

export const stateTone = (s: ClaimState) =>
  s === "acknowledged" || s === "approved" || s === "paid"
    ? "good"
    : s === "rejected" || s === "needs_review"
      ? "bad"
      : "neutral";

export const planName: Record<string, string> = {
  trial: "Free trial",
  flat: "Flat",
  flat_volume: "Flat + volume",
  volume: "Volume",
  enterprise_requested: "Enterprise (requested)",
  enterprise: "Enterprise",
};

export const kindName: Record<ItemKind, string> = {
  service: "Service",
  lab: "Lab test",
  imaging: "Imaging",
  procedure: "Procedure",
  medication: "Medicine",
  consumable: "Consumable",
};

export function itemLabel(it: Item) {
  return `${it.name} (${kindName[it.kind]}, ${it.id})`;
}
