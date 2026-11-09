import { Fragment } from "react";
import { gapLabel, safeHref, utcDateKey, utcDay, utcTime } from "../format";
import { EVENT_LABEL, REASON_LABEL } from "../labels";
import type { TimelineItem } from "../types";

const LATE_AFTER_MS = 60_000;

function glyph(e: TimelineItem): { char: string; tone: string; dashed?: boolean } {
  if (e.link_reason === "repeat_trigger") return { char: "↻", tone: "var(--fg3)", dashed: true };
  switch (e.type) {
    case "deployment.completed":
      return { char: "↑", tone: "var(--accent)" };
    case "error.observed":
      return { char: "!", tone: "var(--major)" };
    case "alert.resolved":
      return { char: "✓", tone: "var(--ok)" };
    default:
      return { char: "◆", tone: "var(--crit)" };
  }
}

function host(href: string): string {
  try {
    return new URL(href).host;
  } catch {
    return "source";
  }
}

/** Every event linked to the incident, oldest first, with the time between
 * events and a day heading whenever the UTC date changes. */
export function Timeline({ items, highlight }: { items: TimelineItem[]; highlight: string | null }) {
  return (
    <ol className="timeline">
      {items.map((e, i) => {
        const prev = items[i - 1];
        const newDay = !prev || utcDateKey(prev.occurred_at) !== utcDateKey(e.occurred_at);
        const g = glyph(e);
        const time = utcTime(e.occurred_at, { seconds: true });
        const late = new Date(e.received_at).getTime() - new Date(e.occurred_at).getTime() > LATE_AFTER_MS;
        const href = safeHref(e.source_url);
        return (
          <Fragment key={e.event_id}>
            {newDay && (
              <li className="tl-day">
                {utcDay(e.occurred_at)}
              </li>
            )}
            {prev && !newDay && (
              <li className="tl-gap" aria-hidden="true">
                <span />
                <span className="tl-gap-line" />
                <span>{gapLabel(prev.occurred_at, e.occurred_at)}</span>
              </li>
            )}
            <li id={`evt-${e.event_id}`} data-testid={`evt-${e.event_id}`} className="tl-row" data-highlight={highlight === e.event_id ? "true" : undefined}>
              <span className="mono tl-time" title={time.title}>
                {time.text.replace(" UTC", "")}
              </span>
              <span
                className="tl-glyph"
                aria-hidden="true"
                style={
                  g.dashed
                    ? { color: g.tone, border: "1px dashed var(--border-strong)" }
                    : { color: g.tone, background: `color-mix(in oklch, ${g.tone} 14%, var(--surface))`, border: `1px solid color-mix(in oklch, ${g.tone} 40%, transparent)` }
                }
              >
                {g.char}
              </span>
              <div className="tl-body">
                <span className="tl-desc">{e.summary || EVENT_LABEL[e.type]}</span>
                <div className="tl-meta">
                  <span>{EVENT_LABEL[e.type]}</span>
                  {e.severity && <span>· {e.severity}</span>}
                  <span>· {REASON_LABEL[e.link_reason]}</span>
                  {late && (
                    <span className="late" title={new Date(e.received_at).toISOString()}>
                      received later · <span className="mono">{utcTime(e.received_at, { seconds: true }).text.replace(" UTC", "")}</span>
                    </span>
                  )}
                  {href && (
                    <a className="mono tl-link" href={href} target="_blank" rel="noopener noreferrer">
                      {host(href)} ↗
                    </a>
                  )}
                </div>
              </div>
            </li>
          </Fragment>
        );
      })}
    </ol>
  );
}
