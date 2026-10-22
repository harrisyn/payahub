import { PlanForm } from "@/components/forms";
import { daysLeft, planName } from "@/lib/format";
import { ghs, monthlyCost, plans } from "@/lib/pricing";
import { requireOrg } from "@/lib/session";

export const metadata = { title: "Plan and usage" };

export default async function Plan() {
  const { organization: org, usage } = await requireOrg();
  const month = new Date(usage.month + "-01").toLocaleDateString("en-GB", { month: "long", year: "numeric" });
  const costs = plans
    .map((p) => ({ p, cost: monthlyCost(p, usage.billable_live) }))
    .filter((x) => x.cost !== null) as { p: (typeof plans)[number]; cost: number }[];

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Plan and usage</h1>
          <p className="app-finding">
            {org.plan === "trial"
              ? `Free trial, ${daysLeft(org.trial_ends_at)} days left. ${usage.live_claims} of ${usage.trial_live_limit} live claims used.`
              : `${planName[org.plan]}. ${usage.billable_live} billable claims so far in ${month}.`}
          </p>
        </div>
      </div>
      <section className="app-card" aria-labelledby="usage-h">
        <h2 id="usage-h" className="app-card__title">
          {month}
        </h2>
        <dl className="app-strip">
          <div>
            <dt>Billable live claims</dt>
            <dd className="num">{usage.billable_live}</dd>
          </div>
          <div>
            <dt>Live claims sent</dt>
            <dd className="num">{usage.live_claims}</dd>
          </div>
          <div>
            <dt>Sandbox claims</dt>
            <dd className="num">{usage.sandbox_claims}</dd>
          </div>
        </dl>
      </section>
      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="plan-h">
          <h2 id="plan-h" className="app-card__title">
            Plan
          </h2>
          <PlanForm current={org.plan} claims={usage.billable_live} />
        </section>
        <section className="app-card" aria-labelledby="cost-h">
          <h2 id="cost-h" className="app-card__title">
            This month on each plan
          </h2>
          <table className="app-table">
            <tbody>
              {costs.map(({ p, cost }) => (
                <tr key={p.id}>
                  <td>{p.name}</td>
                  <td className="num">{ghs(cost)}</td>
                </tr>
              ))}
              <tr>
                <td>Enterprise</td>
                <td className="num app-muted">Agreed</td>
              </tr>
            </tbody>
          </table>
          <p className="app-muted">Worked out from this month&apos;s {usage.billable_live} billable live claims.</p>
        </section>
      </div>
    </div>
  );
}
