import { useEffect, useState } from "react";
import { ApiError, apiFetch, describeError } from "../api";
import { utcTime } from "../format";
import { EVENT_NOUN, KIND_GROUP } from "../labels";
import type { FindingKind, RevisionHistoryItem, RuleRef } from "../types";

const TAG: Record<FindingKind, string> = Object.fromEntries(KIND_GROUP.map((g) => [g.kind, g.tag])) as Record<FindingKind, string>;
const KIND_ORDER = KIND_GROUP.map((g) => g.kind);

/** Why a revision was written, as a sentence. */
export function reasonText(item: RevisionHistoryItem): string {
  switch (item.reason) {
    case "incident_created":
      return "Initial analysis when the incident opened.";
    case "repeat_trigger":
      return "Repeat alert.";
    case "manual_retry":
      return "Retried by an engineer.";
    case "late_event": {
      const first = item.new_evidence[0];
      if (!first) return "Late evidence.";
      const more = item.new_evidence.length - 1;
      return `Late evidence: ${EVENT_NOUN[first.type]} received at ${utcTime(first.received_at, { seconds: true }).text}${more > 0 ? ` and ${more} more` : ""}.`;
    }
  }
}

/** "+ Observed, + Hypothesis, + Check ×2, − Gap": added then removed, by kind. */
export function changesText(item: RevisionHistoryItem): string {
  if (!item.changes) return `Initial analysis · ${item.finding_count} finding${item.finding_count === 1 ? "" : "s"}`;
  const part = (sign: string, refs: RuleRef[]) =>
    KIND_ORDER.flatMap((k) => {
      const n = refs.filter((r) => r.kind === k).length;
      return n === 0 ? [] : [`${sign} ${TAG[k]}${n > 1 ? ` ×${n}` : ""}`];
    });
  const parts = [...part("+", item.changes.added), ...part("−", item.changes.removed)];
  return parts.length ? parts.join(", ") : "No change in findings";
}

/** The incident's revisions, newest first. `refresh` changes when a new
 * revision lands, so the list reloads. */
export function RevisionHistory({
  incidentId,
  refresh,
  viewing,
  onView,
}: {
  incidentId: string;
  refresh: number;
  /** The revision on screen. */
  viewing: number;
  /** n is the revision to show; supersededAt is when the next one replaced it (null for the newest). */
  onView: (n: number, supersededAt: string | null) => void;
}) {
  const [items, setItems] = useState<RevisionHistoryItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let active = true;
    apiFetch<{ items: RevisionHistoryItem[] }>("GET", `/v1/incidents/${incidentId}/revisions`)
      .then((r) => {
        if (!active) return;
        setItems(r.items);
        setError(null);
      })
      .catch((e) => {
        if (!active || (e instanceof ApiError && e.status === 401)) return;
        setError(describeError(e));
      });
    return () => {
      active = false;
    };
  }, [incidentId, refresh, attempt]);

  if (error) {
    return (
      <div className="history-note">
        <span className="error-text">{error}</span>{" "}
        <button type="button" className="ghost" onClick={() => setAttempt((n) => n + 1)}>
          Try again
        </button>
      </div>
    );
  }
  if (!items) return <div className="history-note muted">Loading history…</div>;

  return (
    <ol className="history" aria-label="Revision history">
      {items.map((item, i) => {
        const current = item.revision_number === viewing;
        const t = utcTime(item.created_at, { seconds: true });
        return (
          <li key={item.revision_number} className="history-item">
            <div className="history-head">
              <span className="history-n">Revision {item.revision_number}</span>
              <span className="mono small muted" title={t.title}>
                {t.text}
              </span>
              <button
                type="button"
                className={`history-view${current ? " history-current" : ""}`}
                aria-pressed={current}
                aria-label={current ? `Viewing revision ${item.revision_number}` : `View revision ${item.revision_number}`}
                onClick={() => onView(item.revision_number, i > 0 ? items[i - 1].created_at : null)}
              >
                {current ? "Viewing" : "View"}
              </button>
            </div>
            <span className="history-reason">{reasonText(item)}</span>
            <span className="mono history-changes">{changesText(item)}</span>
          </li>
        );
      })}
    </ol>
  );
}
