import { STEPS } from "../labels";
import type { Analysis } from "../types";

type StepState = "done" | "run" | "wait" | "fail" | "retry";
const ICON: Record<StepState, string> = { done: "✓", run: "●", wait: "○", fail: "✕", retry: "↻" };
const META: Record<StepState, string> = { done: "done", run: "running", wait: "queued", fail: "failed", retry: "waiting to retry" };

/** Each step's state for the newest job. */
function stepStates(a: Analysis): StepState[] {
  const at = a.step ? STEPS.findIndex((s) => s.step === a.step) : -1;
  const here: StepState = a.status === "failed" ? "fail" : a.status === "pending" ? "retry" : "run";
  return STEPS.map((_, i) => (at < 0 ? "wait" : i < at ? "done" : i === at ? here : "wait"));
}

function Steps({ analysis }: { analysis: Analysis }) {
  const states = stepStates(analysis);
  return (
    <ol className="steps" aria-label="Analysis steps">
      {STEPS.map((s, i) => (
        <li key={s.step} className={`step step-${states[i]}`}>
          <span aria-hidden="true" className="step-icon">
            {ICON[states[i]]}
          </span>
          <span className="step-label">{s.label}</span>
          <span className="mono step-meta">{analysis.status === "failed" && states[i] === "wait" ? "not run" : META[states[i]]}</span>
        </li>
      ))}
    </ol>
  );
}

/** Shown when the analysis is not up to date: running, failed or never run. */
export function AnalysisPanel({
  analysis,
  onRetry,
  retrying,
  retryError,
}: {
  analysis: Analysis;
  onRetry: () => void;
  retrying: boolean;
  retryError: string | null;
}) {
  const { status, last_error, latest_revision } = analysis;
  if (status === "pending" || status === "running") {
    return (
      <div className="panel" role="status">
        <div className="panel-text">
          <span className="panel-title">Analysis running</span>
          <span className="muted-2">
            {latest_revision
              ? `An updated analysis is running. The findings below are from revision ${latest_revision.revision_number} and will be replaced when it finishes.`
              : "Findings appear once evidence is correlated. Timeline events show as they arrive."}
          </span>
        </div>
        <div className="progress" aria-hidden="true">
          <span />
        </div>
        <Steps analysis={analysis} />
      </div>
    );
  }
  if (status === "failed" || status === "none") {
    const at = analysis.step ? STEPS.findIndex((s) => s.step === analysis.step) : -1;
    const title =
      status === "none" ? "No analysis has run for this incident yet." : at >= 0 ? `Analysis failed at step ${at + 1} of ${STEPS.length}: ${STEPS[at].label}` : "Analysis failed";
    return (
      <div className={`panel${status === "failed" ? " panel-err" : ""}`}>
        <div className="panel-text">
          <span className="panel-title">{title}</span>
          {status === "failed" && last_error && <span className="mono small">{last_error}</span>}
          {status === "failed" && (
            <span className="muted-2">
              {latest_revision
                ? `The findings below are from revision ${latest_revision.revision_number}.`
                : "No findings were drafted. The timeline below is retrieved evidence, not a conclusion."}
            </span>
          )}
        </div>
        {status === "failed" && at >= 0 && <Steps analysis={analysis} />}
        <div className="panel-actions">
          <button type="button" className="btn btn-dark" onClick={onRetry} disabled={retrying}>
            {retrying ? "Retrying…" : "Retry analysis"}
          </button>
          {retryError && <span className="error-text">{retryError}</span>}
        </div>
      </div>
    );
  }
  return null;
}
