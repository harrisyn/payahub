import { revokeInvitation } from "@/app/actions";
import { Submit } from "@/components/forms";
import { InviteForm, MemberControls } from "@/components/team-forms";
import { api } from "@/lib/api";
import { date } from "@/lib/format";
import { can, roleName, roles } from "@/lib/roles";
import { requireOrg } from "@/lib/session";
import type { Invitation, Member } from "@/lib/types";

export const metadata = { title: "Team" };

export default async function Team() {
  const { organization: org } = await requireOrg();
  const res = await api<{ data: Member[]; invitations: Invitation[] }>(
    "/v1/members",
  );
  if (!res.ok) throw new Error(res.error.message);
  const { data: members, invitations } = res.data;
  const manage = can(org.user.permissions, "team");
  const owner = org.user.role === "owner";
  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>Team</h1>
          <p className="app-finding">
            {members.length} {members.length === 1 ? "person" : "people"} in{" "}
            {org.name}
            {invitations.length > 0 && `, ${invitations.length} invited`}. Your
            role: {roleName[org.user.role]}.
          </p>
        </div>
      </div>

      {manage && (
        <section className="app-card" aria-labelledby="invite-h">
          <div className="app-card__head">
            <h2 id="invite-h" className="app-card__title">
              Invite someone
            </h2>
          </div>
          <p className="app-muted app-measure">
            They get an email with a link to choose a password. The link works
            once, for 7 days.
          </p>
          <InviteForm owner={owner} />
        </section>
      )}

      <section className="app-card" aria-labelledby="members-h">
        <div className="app-card__head">
          <h2 id="members-h" className="app-card__title">
            People
          </h2>
        </div>
        <table className="app-table app-table--stack">
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Role</th>
              <th scope="col">Joined</th>
              {manage && (
                <th scope="col">
                  <span className="sr-only">Change</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.id}>
                <td>
                  <span className="app-strong">{m.name}</span>
                  {m.id === org.user.id && (
                    <span className="app-muted"> (you)</span>
                  )}
                  <span className="app-muted app-block app-wrap">
                    {m.email}
                  </span>
                </td>
                <td>
                  <span
                    className={`app-pill app-pill--${m.role === "owner" ? "good" : "neutral"}`}
                  >
                    {roleName[m.role]}
                  </span>
                </td>
                <td className="app-muted">{date(m.created_at)}</td>
                {manage && (
                  <td className="app-right">
                    <MemberControls
                      id={m.id}
                      role={m.role}
                      owner={owner}
                      self={m.id === org.user.id}
                    />
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      {invitations.length > 0 && (
        <section className="app-card" aria-labelledby="inv-h">
          <div className="app-card__head">
            <h2 id="inv-h" className="app-card__title">
              Waiting to join
            </h2>
          </div>
          <table className="app-table app-table--stack">
            <thead>
              <tr>
                <th scope="col">Invited</th>
                <th scope="col">Role</th>
                <th scope="col">Link expires</th>
                {manage && (
                  <th scope="col">
                    <span className="sr-only">Withdraw</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {invitations.map((i) => (
                <tr key={i.id}>
                  <td>
                    <span className="app-strong app-wrap">
                      {i.name || i.email}
                    </span>
                    <span className="app-muted app-block app-wrap">
                      {i.name ? `${i.email}, ` : ""}invited by {i.invited_by}
                    </span>
                  </td>
                  <td>
                    <span className="app-pill app-pill--neutral">
                      {roleName[i.role]}
                    </span>
                  </td>
                  <td className="app-muted">{date(i.expires_at)}</td>
                  {manage && (
                    <td className="app-right">
                      <form action={revokeInvitation}>
                        <input type="hidden" name="id" value={i.id} />
                        <Submit className="app-button app-button--danger">
                          Withdraw
                        </Submit>
                      </form>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      <section className="app-card" aria-labelledby="roles-h">
        <div className="app-card__head">
          <h2 id="roles-h" className="app-card__title">
            What each role can do
          </h2>
        </div>
        <p className="app-muted app-measure">
          Everyone can see the organisation&rsquo;s claims, insurers, prices and
          wallet. Roles decide who changes them. API keys are not people: a key
          works for the whole organisation in its environment.
        </p>
        <dl className="app-roles">
          {roles.map((r) => (
            <div key={r.id}>
              <dt>{r.name}</dt>
              <dd>{r.does}</dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  );
}
