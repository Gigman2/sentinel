import { EVENT_LABEL } from "../labels";
import { utcTime } from "../format";
import type { TimelineItem } from "../types";

/** A citation: jumps to the cited event's timeline row. */
export function EvidenceChip({ eventId, event, onCite }: { eventId: string; event?: TimelineItem; onCite: (id: string) => void }) {
  const label = event ? `${EVENT_LABEL[event.type]} · ${utcTime(event.occurred_at).text}` : "Event";
  return (
    <button type="button" className="ev" onClick={() => onCite(eventId)} title={event ? event.summary : "Not on the loaded timeline"}>
      <span aria-hidden="true">↗</span> {label}
    </button>
  );
}
