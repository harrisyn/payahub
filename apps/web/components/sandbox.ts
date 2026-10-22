// The sandbox payer's rules, mirrored for the landing page. They are the same
// rules as internal/connector/sandbox in the Go API: the member number's
// prefix picks the path, so a visitor sees exactly what their trial will do.

export type Step = { kind: string; detail: string };
export type SandboxRun = {
  member: { ok: boolean; text: string };
  outcome: { tone: "good" | "bad" | "wait"; text: string };
  events: Step[];
  sends: number;
};

async function claimNumber(memberNumber: string): Promise<string> {
  const data = new TextEncoder().encode("clm_" + memberNumber.toUpperCase());
  const sum = new Uint8Array(await crypto.subtle.digest("SHA-256", data));
  const hex = Array.from(sum.slice(0, 5), (b) => b.toString(16).padStart(2, "0")); // taste-skill-ignore: hex digits
  return "SBX-" + hex.join("").toUpperCase();
}

export async function runSandbox(raw: string): Promise<SandboxRun> {
  const member = raw.trim() || "M-1001";
  const upper = member.toUpperCase();
  const number = await claimNumber(member);
  const created: Step[] = [
    { kind: "created", detail: "Draft, GHS 150.00, one line" },
    { kind: "submission_queued", detail: "Checked and queued in one transaction" },
    { kind: "submission_started", detail: "Attempt 1" },
  ];

  if (upper.startsWith("NOTFOUND")) {
    return {
      member: { ok: false, text: `No member ${member} at this insurer` },
      outcome: { tone: "bad", text: "Rejected: member not found. Fix the number and send again; nothing is billed." },
      events: [...created, { kind: "rejected", detail: "member_not_found" }],
      sends: 1,
    };
  }
  if (upper.startsWith("REJECT")) {
    return {
      member: { ok: true, text: "Eligible, Sandbox Gold plan" },
      outcome: { tone: "bad", text: "Refused by the insurer: “Member benefit exhausted for this period.”" },
      events: [...created, { kind: "rejected", detail: "payer_refused" }],
      sends: 1,
    };
  }
  if (upper.startsWith("OTP")) {
    return {
      member: { ok: true, text: "Eligible once the member reads you the code sent to their phone" },
      outcome: { tone: "good", text: `Acknowledged after the code was entered, claim ${number}` },
      events: [...created, { kind: "acknowledged", detail: number }],
      sends: 1,
    };
  }
  if (upper.startsWith("FLAKY")) {
    return {
      member: { ok: true, text: "Eligible, Sandbox Gold plan" },
      outcome: { tone: "good", text: `Insurer timed out, retried after a pause, acknowledged on attempt 2: ${number}` },
      events: [
        ...created,
        { kind: "attempt_failed", detail: "timeout, will retry" },
        { kind: "acknowledged", detail: number },
      ],
      sends: 2,
    };
  }
  if (upper.startsWith("LOSTREPLY")) {
    return {
      member: { ok: true, text: "Eligible, Sandbox Gold plan" },
      outcome: { tone: "good", text: `Reply lost on the way back; the retry found ${number} at the insurer and did not send it again` },
      events: [
        ...created,
        { kind: "attempt_failed", detail: "reply lost after the insurer accepted" },
        { kind: "acknowledged", detail: `${number}, found by lookup` },
      ],
      sends: 1,
    };
  }
  return {
    member: { ok: true, text: "Eligible, Sandbox Gold plan" },
    outcome: { tone: "good", text: `Acknowledged by the insurer, claim ${number}` },
    events: [...created, { kind: "acknowledged", detail: number }],
    sends: 1,
  };
}
