// Plans and sample price points. The four plan shapes are decided; the GHS
// figures are placeholders until pricing interviews set them (see the plan doc).
export type PlanId = "flat" | "flat_volume" | "volume" | "enterprise";

export type Plan = {
  id: PlanId;
  name: string;
  monthlyFee: number | null; // GHS per month, null when agreed in contract
  perClaim: number | null; // GHS per acknowledged claim
  included: number; // acknowledged claims covered by the monthly fee (fair use)
  summary: string;
};

export const TRIAL_DAYS = 14;
export const TRIAL_LIVE_CLAIMS = 50;

export const plans: Plan[] = [
  {
    id: "flat",
    name: "Flat",
    monthlyFee: 1800,
    perClaim: null,
    included: 1500,
    summary:
      "One fee a month for up to 1,500 acknowledged claims, every insurer we support, and the full API. Paid monthly in advance by card or mobile money.",
  },
  {
    id: "flat_volume",
    name: "Flat + volume",
    monthlyFee: 600,
    perClaim: 1,
    included: 0,
    summary:
      "A lower monthly fee plus GHS 1.00 for each claim an insurer acknowledges. The fee is paid in advance and the claims at the end of the month.",
  },
  {
    id: "volume",
    name: "Volume",
    monthlyFee: null,
    perClaim: 2,
    included: 0,
    summary:
      "No monthly fee. GHS 2.00 for each claim an insurer acknowledges, billed at the end of the month. Quiet months cost little.",
  },
  {
    id: "enterprise",
    name: "Enterprise",
    monthlyFee: null,
    perClaim: null,
    included: 0,
    summary:
      "Terms agreed with your group, invoiced offline. Usage is still counted and shown in your console, claim by claim.",
  },
];

// monthlyCost is what a plan costs for a month with `claims` acknowledged claims;
// null for enterprise, which is agreed in contract.
export function monthlyCost(plan: Plan, claims: number): number | null {
  if (plan.id === "enterprise") return null;
  const fee = plan.monthlyFee ?? 0;
  const billable = Math.max(0, claims - plan.included);
  if (plan.id === "flat") return fee; // fair use above the included claims
  return fee + billable * (plan.perClaim ?? 0);
}

export const ghs = (n: number) =>
  "GHS " + n.toLocaleString("en-GH", { minimumFractionDigits: 0, maximumFractionDigits: 2 });
