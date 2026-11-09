import type { Finding, TimelineItem } from "../types";
import { EvidenceChip } from "./EvidenceChip";

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
  return (
    <li className={`frow frow-${kind}`}>
      <div className="frow-main">
        {kind === "next_check" && !tag && <span className="check-mark" aria-hidden="true">→</span>}
        {tag && <span className={`tag tag-${kind}`}>{tag}</span>}
        <div className="frow-text">
          <span className="ftext">
            {kind === "hypothesis" && <span className="possible">Possible: </span>}
            {finding.text}
          </span>
          {finding.limitations && (
            <span className="fsub">
              Limitations: <span>{finding.limitations}</span>
            </span>
          )}
        </div>
      </div>
      {cited.length > 0 && (
        <div className="ev-row">
          <span className="ev-word">{kind === "hypothesis" ? "Rests on" : "Evidence"}</span>
          {cited.map((id) => (
            <EvidenceChip key={id} eventId={id} event={events.get(id)} onCite={onCite} />
          ))}
        </div>
      )}
    </li>
  );
}
