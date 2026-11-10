import { SOURCE_STATUS_LABEL, SOURCE_STATUS_TONE } from "../labels";
import type { EvidenceSource } from "../types";
import { Dot } from "./Dot";

function detail(s: EvidenceSource): string {
  if (s.status === "not_connected") return "—";
  return `${s.event_count} event${s.event_count === 1 ? "" : "s"}`;
}

/** Which sources returned data for this incident: a coverage bar with one
 * segment per source, then each source with its status. */
export function EvidenceSources({ sources }: { sources: EvidenceSource[] }) {
  const returned = sources.filter((s) => s.status === "retrieved").length;
  return (
    <section aria-labelledby="sources-h" className="side-section">
      <div className="side-title-row">
        <h2 id="sources-h" className="side-title">
          Evidence sources
        </h2>
        <span className="mono small muted-2">
          {returned} of {sources.length} returned data
        </span>
      </div>
      <div className="coverage" aria-hidden="true">
        {sources.map((s, i) => (
          <span key={s.integration_id ?? `${s.name}-${i}`} style={{ background: s.status === "not_connected" ? "var(--surface3)" : SOURCE_STATUS_TONE[s.status] }} />
        ))}
      </div>
      <ul className="sources">
        {sources.map((s, i) => (
          <li key={s.integration_id ?? `${s.name}-${i}`} className="source">
            <span className="source-name">
              <span>{s.name}</span>
              <span className="mono small muted">{detail(s)}</span>
            </span>
            <span className="inline-dot muted-2">
              <Dot color={SOURCE_STATUS_TONE[s.status]} />
              {SOURCE_STATUS_LABEL[s.status]}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
