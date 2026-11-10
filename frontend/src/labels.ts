// Human labels for API values.
import type { AnalysisStatus, Disposition, EventType, FindingKind, LinkReason } from "./types";

export const EVENT_LABEL: Record<EventType, string> = {
  "alert.triggered": "Alert",
  "alert.resolved": "Alert resolved",
  "error.observed": "Error event",
  "deployment.completed": "Deployment",
};

export const REASON_LABEL: Record<LinkReason, string> = {
  trigger: "Opened the incident",
  repeat_trigger: "Repeat alert, joined the incident",
  resolution: "Resolved the incident",
  same_component_time_window: "Same component, in window",
};

export const KIND_GROUP: { kind: FindingKind; label: string; tag: string; hint: string }[] = [
  { kind: "observation", label: "Observed", tag: "Observed", hint: "backed by ingested events" },
  { kind: "hypothesis", label: "Hypothesis", tag: "Hypothesis", hint: "possible, not confirmed" },
  { kind: "unknown", label: "Gaps", tag: "Gap", hint: "missing data" },
  { kind: "next_check", label: "Check next", tag: "Check", hint: "" },
];

export const ANALYSIS_LABEL: Record<AnalysisStatus, string> = {
  none: "Not run",
  pending: "Pending",
  running: "Running",
  succeeded: "Complete",
  failed: "Failed",
};

/** Colour token for an analysis status dot. */
export const ANALYSIS_TONE: Record<AnalysisStatus, string> = {
  none: "var(--fg3)",
  pending: "var(--accent)",
  running: "var(--accent)",
  succeeded: "var(--ok)",
  failed: "var(--crit)",
};

export const CAPABILITY_LABEL: Record<string, string> = {
  incident_input: "Incident input",
  change_source: "Change source",
};

/** What grouping did with an alert, as shown on the Alerts page. */
export const DISPOSITION_LABEL: Record<Disposition, string> = {
  incident_created: "Opened",
  incident_attached: "Grouped into",
  incident_resolved: "Resolved",
  unmatched_resolution: "No open incident",
  evidence: "Evidence",
};
