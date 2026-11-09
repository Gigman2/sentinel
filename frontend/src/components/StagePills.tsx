import type { Incident } from "../types";
import { Dot } from "./Dot";

type State = "done" | "run" | "wait" | "fail" | "now";

/** Where the incident is: signals grouped → evidence → findings → resolution,
 * derived from the analysis status and the incident status. */
export function stages(inc: Incident): { label: string; state: State }[] {
  const a = inc.analysis.status;
  const busy = a === "pending" || a === "running";
  return [
    { label: "Signals grouped", state: "done" },
    busy ? { label: "Collecting evidence", state: "run" } : { label: "Evidence collected", state: a === "none" ? "wait" : "done" },
    a === "failed"
      ? { label: "Findings failed", state: "fail" }
      : { label: "Findings drafted", state: a === "succeeded" ? "done" : "wait" },
    inc.status === "resolved" ? { label: "Resolved", state: "done" } : { label: "Awaiting resolution", state: "now" },
  ];
}

const DOT: Record<State, { color: string; hollow?: boolean; pulse?: boolean }> = {
  done: { color: "var(--ok)" },
  run: { color: "var(--accent)", pulse: true },
  wait: { color: "var(--border-strong)", hollow: true },
  fail: { color: "var(--crit)" },
  now: { color: "var(--fg2)", hollow: true },
};

export function StagePills({ incident }: { incident: Incident }) {
  return (
    <ol className="stages" aria-label="Progress">
      {stages(incident).map((s) => (
        <li key={s.label} className={`stage stage-${s.state}`}>
          <Dot {...DOT[s.state]} />
          {s.label}
        </li>
      ))}
    </ol>
  );
}
