import type { Analysis } from "../types";

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
      </div>
    );
  }
  if (status === "failed" || status === "none") {
    return (
      <div className={`panel${status === "failed" ? " panel-err" : ""}`}>
        <div className="panel-text">
          <span className="panel-title">{status === "failed" ? "Analysis failed" : "No analysis has run for this incident yet."}</span>
          {status === "failed" && last_error && <span className="mono small">{last_error}</span>}
          {status === "failed" && (
            <span className="muted-2">
              {latest_revision
                ? `The findings below are from revision ${latest_revision.revision_number}.`
                : "No findings were drafted. The timeline below is retrieved evidence, not a conclusion."}
            </span>
          )}
        </div>
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
