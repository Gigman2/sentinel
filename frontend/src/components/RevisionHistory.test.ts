import { describe, expect, it } from "vitest";
import type { RevisionHistoryItem } from "../types";
import { changesText, reasonText } from "./RevisionHistory";

const late = (new_evidence: RevisionHistoryItem["new_evidence"]): RevisionHistoryItem => ({
  revision_number: 2,
  created_at: "2026-11-09T14:11:20Z",
  reason: "late_event",
  finding_count: 7,
  changes: { added: [], removed: [] },
  new_evidence,
});

describe("revision history text", () => {
  it("names the late event, not a repeat alert that arrived first", () => {
    const item = late([
      { event_id: "a", type: "alert.triggered", received_at: "2026-11-09T14:10:50Z" },
      { event_id: "d", type: "deployment.completed", received_at: "2026-11-09T14:11:03Z" },
    ]);
    expect(reasonText(item)).toBe("Late evidence: deployment received at 14:11:03 UTC.");
  });

  it("counts further late events", () => {
    const item = late([
      { event_id: "d", type: "deployment.completed", received_at: "2026-11-09T14:11:03Z" },
      { event_id: "e", type: "error.observed", received_at: "2026-11-09T14:11:09Z" },
    ]);
    expect(reasonText(item)).toBe("Late evidence: deployment received at 14:11:03 UTC and 1 more.");
  });

  it("says only 'Late evidence.' when what was new is unknown", () => {
    expect(reasonText(late(null))).toBe("Late evidence.");
  });

  it("reports no change when the findings are the same", () => {
    expect(changesText(late([]))).toBe("No change in findings");
  });
});
