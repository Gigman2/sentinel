import { useEffect, useState } from "react";
import { ApiError, apiFetch, describeError } from "../api";
import { utcTime } from "../format";
import type { CheckStatus, Finding, FindingFeedback, TimelineItem, Verdict } from "../types";
import { EvidenceChip } from "./EvidenceChip";

/** The feedback after the viewer changes their verdict to `next` (null clears it). */
function withVerdict(fb: FindingFeedback, next: Verdict | null): FindingFeedback {
  const out = { ...fb, mine: next };
  if (fb.mine) out[fb.mine] -= 1;
  if (next) out[next] += 1;
  return out;
}

export function FindingRow({
  finding,
  tag,
  events,
  onCite,
}: {
  finding: Finding;
  tag: string | null;
  events: Map<string, TimelineItem>;
  onCite: (id: string) => void;
}) {
  const kind = finding.kind;
  const cited = [...new Set(finding.evidence_ids)];

  // Shown immediately on click and rolled back if the request fails. A
  // fresh read (polling, a new revision) replaces them unless a request is
  // in flight.
  const [fb, setFb] = useState(finding.feedback);
  const [check, setCheck] = useState<CheckStatus | null>(finding.check);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (busy) return;
    setFb(finding.feedback);
    setCheck(finding.check);
  }, [finding.feedback, finding.check]); // busy is read, not watched: a finished request must not reset

  async function vote(v: Verdict) {
    const prev = fb;
    const next = fb.mine === v ? null : v; // clicking your verdict again clears it
    setFb(withVerdict(prev, next));
    setBusy(true);
    setError(null);
    try {
      const path = `/v1/findings/${finding.id}/feedback`;
      if (next) setFb(await apiFetch<FindingFeedback>("PUT", path, { verdict: next }));
      else await apiFetch("DELETE", path);
    } catch (e) {
      setFb(prev);
      if (!(e instanceof ApiError && e.status === 401)) setError(`Feedback wasn't saved: ${describeError(e)}`);
    } finally {
      setBusy(false);
    }
  }

  async function toggleDone() {
    if (!check) return;
    const prev = check;
    const done = !check.done;
    setCheck(done ? { done: true, done_by: null, done_at: null } : { done: false, done_by: null, done_at: null });
    setBusy(true);
    setError(null);
    try {
      setCheck(await apiFetch<CheckStatus>("PUT", `/v1/findings/${finding.id}/check`, { done }));
    } catch (e) {
      setCheck(prev);
      if (!(e instanceof ApiError && e.status === 401)) setError(`Couldn't update the check: ${describeError(e)}`);
    } finally {
      setBusy(false);
    }
  }

  const doneTitle = check?.done && check.done_by ? `Done by ${check.done_by.display_name} at ${utcTime(check.done_at, { seconds: true }).text}` : undefined;
  const buttons = (
    <span className="fb-group" role="group" aria-label="Feedback on this finding">
      {(["useful", "incorrect"] as const).map((v) => (
        <button
          key={v}
          type="button"
          className={`fb fb-${v}${fb.mine === v ? " fb-on" : ""}`}
          aria-pressed={fb.mine === v}
          disabled={busy}
          title={`${fb[v]} engineer${fb[v] === 1 ? "" : "s"} marked this ${v}`}
          onClick={() => vote(v)}
        >
          {v === "useful" ? "Useful" : "Incorrect"}
          {fb[v] > 0 && <span className="fb-count">{fb[v]}</span>}
        </button>
      ))}
    </span>
  );

  return (
    <li className={`frow frow-${kind}`}>
      <div className="frow-main">
        {kind === "next_check" && check && (
          <button
            type="button"
            role="checkbox"
            aria-checked={check.done}
            aria-label={check.done ? "Done; mark not done" : "Mark done"}
            className={`check-box${check.done ? " check-on" : ""}`}
            disabled={busy}
            title={doneTitle}
            onClick={toggleDone}
          >
            {check.done ? "✓" : ""}
          </button>
        )}
        {kind === "next_check" && !check && !tag && (
          <span className="check-mark" aria-hidden="true">
            →
          </span>
        )}
        {tag && <span className={`tag tag-${kind}`}>{tag}</span>}
        <div className="frow-text">
          <span className={`ftext${check?.done ? " ftext-done" : ""}`} title={doneTitle}>
            {kind === "hypothesis" && <span className="possible">Possible: </span>}
            {finding.text}
          </span>
          {finding.limitations && (
            <span className="fsub">
              Limitations: <span>{finding.limitations}</span>
            </span>
          )}
        </div>
        {cited.length === 0 && buttons}
      </div>
      {cited.length > 0 && (
        <div className="ev-row">
          <span className="ev-word">{kind === "hypothesis" ? "Rests on" : "Evidence"}</span>
          {cited.map((id) => (
            <EvidenceChip key={id} eventId={id} event={events.get(id)} onCite={onCite} />
          ))}
          <span className="grow" />
          {buttons}
        </div>
      )}
      {fb.mine === "incorrect" && <span className="fnote">Marked incorrect. Recorded for review; the finding stays visible.</span>}
      {error && (
        <span className="fnote error-text" role="alert">
          {error}
        </span>
      )}
    </li>
  );
}
