import { capitalise } from "../format";
import type { Severity } from "../types";

const BARS: Record<Severity, number> = { critical: 3, error: 3, warning: 2, info: 1 };

/** The design's 3-bar severity glyph with its label. Critical on an open
 * incident gets the filled red chip; a resolved incident is muted. */
export function SeverityChip({ severity, muted = false }: { severity: Severity | ""; muted?: boolean }) {
  if (!severity) return <span className="sev-chip sev-none">–</span>;
  const n = BARS[severity];
  const loud = severity === "critical" && !muted;
  return (
    <span className={`sev-chip${loud ? " sev-loud" : ""}${muted ? " sev-muted" : ""}`} data-severity={severity}>
      <span className="sev-bars" aria-hidden="true">
        {[5, 8, 11].map((h, k) => (
          <span key={h} style={{ height: h }} className={k < n ? "on" : ""} />
        ))}
      </span>
      {capitalise(severity)}
    </span>
  );
}
