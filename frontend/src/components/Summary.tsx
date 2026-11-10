import type { AnalysisStatus, AnalysisStep, Revision } from "../types";

/** The revision's summary, written by the worker only from its findings.
 * Times are set in mono and "Cause: Unknown." in bold; every segment is a
 * text node. */
export function Summary({
  revision,
  status,
  step,
  resolved,
  loadingOld,
}: {
  revision: Revision | null;
  status: AnalysisStatus;
  /** The step the newest job is on or failed at. */
  step: AnalysisStep | null;
  resolved: boolean;
  /** An older revision being fetched (revision is null until it arrives). */
  loadingOld?: { n: number; error: string | null } | null;
}) {
  const segments = revision?.summary ?? [];
  let meta: string;
  let body;
  if (revision && segments.length > 0) {
    meta = `Built only from the ${revision.findings.length} finding${revision.findings.length === 1 ? "" : "s"} below`;
    body = segments.map((s, i) =>
      s.kind === "time" ? (
        <span key={i} className="mono summary-time">
          {s.text}
        </span>
      ) : s.kind === "strong" ? (
        <b key={i}>{s.text}</b>
      ) : (
        <span key={i}>{s.text}</span>
      ),
    );
  } else if (loadingOld) {
    meta = "Not available yet";
    body = <span className="muted-2">{loadingOld.error ? `Revision ${loadingOld.n} couldn't load.` : `Loading revision ${loadingOld.n}…`}</span>;
  } else if (revision) {
    meta = "Not available";
    body = <span className="muted-2">No summary was written for this revision. The findings below still apply.</span>;
  } else if (status === "pending" || status === "running") {
    meta = "Not available yet";
    body = <span className="muted-2">Summary pending. It will be written only from findings once analysis completes.</span>;
  } else if (status === "failed") {
    meta = "Not available";
    // Match the step the analysis failed at, as the panel above names it.
    const why =
      step === "save_revision"
        ? "Analysis failed after drafting findings, before they were saved"
        : step === "collect_evidence" || step === "draft_findings"
          ? "Analysis failed before findings were drafted"
          : "Analysis failed";
    body = <span className="muted-2">No summary. {why}; the timeline below is retrieved evidence, not a conclusion.</span>;
  } else {
    meta = "Not available";
    body = <span className="muted-2">No summary. No analysis has run for this incident yet.</span>;
  }

  return (
    <section className="block" aria-labelledby="summary-h">
      <div className="block-head">
        <h2 id="summary-h">Summary</h2>
        <span className="muted small">{meta}</span>
        <span className="mono muted small summary-rev">{revision ? `Revision ${revision.revision_number}` : loadingOld ? `Revision ${loadingOld.n}` : "No revision yet"}</span>
      </div>
      <div className={`summary${resolved ? " summary-resolved" : ""}`}>
        <p>{body}</p>
      </div>
    </section>
  );
}
