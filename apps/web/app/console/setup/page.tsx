import { chooseMethod } from "@/app/actions";
import { ProfileForm, Submit } from "@/components/forms";
import { api } from "@/lib/api";
import { methodName } from "@/lib/format";
import { requireOrg } from "@/lib/session";
import type { Method, Payer } from "@/lib/types";

export const metadata = { title: "How you bill" };

const methodDetail: Record<Method, string> = {
  rest: "Your system calls our API once per claim. Fastest, no retyping, and every insurer through one integration.",
  batch: "Upload the spreadsheet you already make; each row becomes a claim, checked before it is sent.",
  documents: "Upload scans or PDFs of claim forms; we draft each claim for you to confirm.",
  web_form: "Type each claim once into a form that checks it as you go.",
};

export default async function Setup() {
  const { organization: org, recommendation } = await requireOrg();
  const payers = await api<{ data: Payer[] }>("/v1/payers");
  const list = payers.ok ? payers.data.data : [];
  const chosen = org.profile?.method ?? recommendation?.method;

  return (
    <div className="app-page">
      <div className="app-title">
        <div>
          <h1>How you bill today</h1>
          <p className="app-finding">
            {recommendation
              ? `We recommend ${methodName[recommendation.method]}${recommendation.available ? "." : ", which is in build; start on the API meanwhile."}`
              : "Answer four questions and we recommend how to connect. Nothing is final; you can change it later."}
          </p>
        </div>
      </div>

      <div className="app-split app-split--wide">
        <section className="app-card" aria-labelledby="q-h">
          <h2 id="q-h" className="app-card__title">
            Four questions
          </h2>
          <ProfileForm profile={org.profile} payers={list} />
        </section>

        <section className="app-card" aria-labelledby="rec-h">
          <h2 id="rec-h" className="app-card__title">
            How to connect
          </h2>
          {recommendation ? (
            <>
              <div className="app-rec">
                <p className="app-rec__method">{methodName[recommendation.method]}</p>
                <span className={`app-pill app-pill--${recommendation.available ? "good" : "wait"}`}>
                  {recommendation.available ? "Ready today" : "In build"}
                </span>
              </div>
              <p>{recommendation.reason}</p>
              <ul className="app-methods">
                {(Object.keys(methodName) as Method[]).map((m) => (
                  <li key={m} className={m === chosen ? "is-chosen" : ""}>
                    <div>
                      <p className="app-step__title">
                        {methodName[m]}
                        {m === recommendation.method && <span className="app-tag">Recommended</span>}
                        {m === "rest" && m !== recommendation.method && <span className="app-tag">Ready today</span>}
                      </p>
                      <p className="app-muted">{methodDetail[m]}</p>
                    </div>
                    {m === chosen ? (
                      <span className="app-muted">Chosen</span>
                    ) : (
                      <form action={chooseMethod}>
                        <input type="hidden" name="method" value={m} />
                        <Submit className="app-button">Use this</Submit>
                      </form>
                    )}
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <p className="app-empty">Your recommendation appears here once you save your answers.</p>
          )}
        </section>
      </div>
    </div>
  );
}
