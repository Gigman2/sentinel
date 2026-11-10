// Shapes returned by the Sentinel API (backend/api/internal/modules/*).

export type Severity = "info" | "warning" | "error" | "critical";
export type IncidentStatus = "open" | "resolved";
export type AnalysisStatus = "none" | "pending" | "running" | "succeeded" | "failed";
export type FindingKind = "observation" | "hypothesis" | "unknown" | "next_check";
export type EventType = "alert.triggered" | "alert.resolved" | "error.observed" | "deployment.completed";
export type LinkReason = "trigger" | "repeat_trigger" | "resolution" | "same_component_time_window";
export type Disposition = "incident_created" | "incident_attached" | "incident_resolved" | "unmatched_resolution" | "evidence";

export interface Project {
  id: string;
  slug: string;
  name: string;
  created_at: string;
}

export interface ComponentRef {
  id?: string;
  slug: string;
  display_name: string;
}

export interface IncidentListItem {
  id: string;
  status: IncidentStatus;
  severity: Severity | "";
  title: string;
  component: ComponentRef;
  environment: string;
  first_seen_at: string;
  last_seen_at: string;
  number: number;
  analysis_status: AnalysisStatus;
  signal_count: number;
  resolution_note: string | null;
}

export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

export interface IncidentPage extends Page<IncidentListItem> {
  /** Open incidents in the project, whatever the filters. */
  open_count: number;
}

export interface ProjectComponent {
  id: string;
  slug: string;
  display_name: string;
  environment: string;
}

/** An alert received for a project, and what grouping did with it. */
export interface Signal {
  event_id: string;
  type: "alert.triggered" | "alert.resolved";
  occurred_at: string;
  received_at: string;
  summary: string;
  severity: Severity | null;
  component: ComponentRef;
  environment: string;
  disposition: Disposition;
  incident: { id: string; number: number } | null;
  source_url: string | null;
}

export interface Finding {
  kind: FindingKind;
  rule_id: string;
  text: string;
  limitations: string | null;
  evidence_ids: string[];
}

export interface Revision {
  id: string;
  revision_number: number;
  created_at: string;
  findings: Finding[];
}

export interface Analysis {
  status: AnalysisStatus;
  last_error: string | null;
  /** The newest job's first lease; null before it starts (or for jobs from
   * before start times were recorded). */
  started_at: string | null;
  /** When the newest job succeeded or failed. */
  finished_at: string | null;
  revision_count: number;
  latest_revision: Revision | null;
}

export type AnalysisReason = "incident_created" | "repeat_trigger" | "late_event" | "manual_retry";

export interface RuleRef {
  kind: FindingKind;
  rule_id: string;
}

/** One revision in an incident's history (GET /v1/incidents/{id}/revisions). */
export interface RevisionHistoryItem {
  revision_number: number;
  created_at: string;
  reason: AnalysisReason;
  finding_count: number;
  /** null for revision 1, which has nothing to compare against. */
  changes: { added: RuleRef[]; removed: RuleRef[] } | null;
  /** Events this revision read that the previous one did not; null when
   * unknown (written before revisions recorded what they read). */
  new_evidence: { event_id: string; type: EventType; received_at: string }[] | null;
}

export type EvidenceSourceStatus = "retrieved" | "no_matching_data" | "not_connected";

export interface EvidenceSource {
  integration_id: string | null;
  name: string;
  capability: "incident_input" | "change_source" | null;
  event_count: number;
  status: EvidenceSourceStatus;
}

export interface Incident {
  id: string;
  number: number;
  project_id: string;
  component: ComponentRef;
  environment: string;
  fingerprint: string;
  status: IncidentStatus;
  severity: Severity | "";
  title: string;
  first_seen_at: string;
  last_seen_at: string;
  evidence_window: { start: string; end: string };
  resolved_at: string | null;
  resolution: { source: "alert" | "manual"; note: string | null } | null;
  resolved_by: { id: string; display_name: string } | null;
  trigger_event_id: string;
  signal_count: number;
  analysis: Analysis;
  evidence_sources: EvidenceSource[];
}

export interface TimelineItem {
  event_id: string;
  type: EventType;
  occurred_at: string;
  received_at: string;
  summary: string;
  severity: string | null;
  source_url: string | null;
  link_reason: LinkReason;
  attributes: Record<string, unknown>;
  details: Record<string, unknown>;
}

export interface Integration {
  id: string;
  project_id: string;
  name: string;
  capability: "incident_input" | "change_source";
  enabled: boolean;
  created_at: string;
  mappings: { id: string; external_component_id: string; environment: string; component_id: string }[];
}
