import { KIND_GROUP } from "../labels";
import type { Finding, TimelineItem } from "../types";
import { Dot } from "./Dot";
import { FindingRow } from "./FindingRow";

const TONE: Record<string, string> = {
  observation: "var(--accent)",
  hypothesis: "var(--major)",
  unknown: "var(--fg3)",
  next_check: "var(--ok)",
};

/** Findings by type (the default) or as one stream in the analysis's order.
 * Kinds are told apart by their tag text and border style, not only colour. */
export function Findings({
  findings,
  events,
  layout,
  onCite,
}: {
  findings: Finding[];
  events: Map<string, TimelineItem>;
  layout: "grouped" | "stream";
  onCite: (id: string) => void;
}) {
  if (layout === "stream") {
    return (
      <div className="fgroup">
        <ul className="flist">
          {findings.map((f, i) => (
            <FindingRow key={i} finding={f} tag={KIND_GROUP.find((g) => g.kind === f.kind)!.tag} events={events} onCite={onCite} />
          ))}
        </ul>
      </div>
    );
  }
  return (
    <div className="fgroups">
      {KIND_GROUP.map((g) => {
        const items = findings.filter((f) => f.kind === g.kind);
        if (items.length === 0) return null;
        return (
          <section key={g.kind} className="fgroup" aria-label={g.label}>
            <div className="fgroup-head">
              <Dot color={TONE[g.kind]} />
              <h3>{g.label}</h3>
              <span className="mono muted">{items.length}</span>
              {g.hint && <span className="muted small">{g.hint}</span>}
            </div>
            <ul className="flist">
              {items.map((f, i) => (
                <FindingRow key={i} finding={f} tag={g.kind === "next_check" ? null : g.tag} events={events} onCite={onCite} />
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
