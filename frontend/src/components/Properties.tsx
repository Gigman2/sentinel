import type { ReactNode } from "react";
import { utcTime } from "../format";
import { ANALYSIS_LABEL, ANALYSIS_TONE, EVENT_LABEL } from "../labels";
import type { Incident, TimelineItem } from "../types";
import { Dot } from "./Dot";
import { SeverityChip } from "./SeverityChip";

function Prop({ k, children }: { k: string; children: ReactNode }) {
  return (
    <div className="prop">
      <span>{k}</span>
      <span>{children}</span>
    </div>
  );
}

function Time({ iso, seconds = true, zone = true }: { iso: string | null; seconds?: boolean; zone?: boolean }) {
  const t = utcTime(iso, { seconds });
  return (
    <span className="mono" title={t.title}>
      {zone ? t.text : t.text.replace(" UTC", "")}
    </span>
  );
}

/** The detail page's side column: properties, analysis and grouped signals. */
export function Properties({ incident, events, onCite }: { incident: Incident; events: TimelineItem[]; onCite: (id: string) => void }) {
  const trigger = events.find((e) => e.event_id === incident.trigger_event_id);
  const signals = events.filter((e) => e.link_reason === "trigger" || e.link_reason === "repeat_trigger");
  const a = incident.analysis;
  const resolved = incident.status === "resolved";
  const win = incident.evidence_window;
  return (
    <aside className="detail-side">
      <section aria-label="Properties" className="side-section">
        <h2 className="side-title">Properties</h2>
        <Prop k="Status">
          <span className="chip">
            <Dot color={resolved ? "var(--ok)" : "var(--fg2)"} size={8} hollow={!resolved} />
            {resolved ? "Resolved" : "Open"}
          </span>
        </Prop>
        <Prop k="Severity">
          <SeverityChip severity={incident.severity} muted={resolved} />
        </Prop>
        <Prop k="Component">
          <span className="chip mono" title={incident.component.display_name}>
            {incident.component.slug}
          </span>
        </Prop>
        <Prop k="Environment">
          <span className="chip">{incident.environment}</span>
        </Prop>
        <Prop k="First seen">
          <Time iso={incident.first_seen_at} />
        </Prop>
        <Prop k="Last seen">
          <Time iso={incident.last_seen_at} />
        </Prop>
        <Prop k="Investigated window">
          <span className="mono" title={`${win.start} – ${win.end}`}>{`${utcTime(win.start).text} – ${utcTime(win.end).text} UTC`}</span>
        </Prop>
        <Prop k="Trigger">
          {trigger ? (
            <button type="button" className="ghost trigger-link" onClick={() => onCite(trigger.event_id)}>
              <span>{EVENT_LABEL[trigger.type]}</span>
              <span className="mono small muted-2">{trigger.summary}</span>
            </button>
          ) : (
            <span className="muted">Loading…</span>
          )}
        </Prop>
        {resolved && (
          <Prop k="Resolved">
            <Time iso={incident.resolved_at} />
            {incident.resolution?.source === "alert" && <span className="muted small"> by its alert</span>}
          </Prop>
        )}
      </section>

      <section aria-label="Analysis" className="side-section">
        <h2 className="side-title">Analysis</h2>
        <Prop k="Status">
          <span className="inline-dot">
            <Dot color={ANALYSIS_TONE[a.status]} pulse={a.status === "pending" || a.status === "running"} />
            {ANALYSIS_LABEL[a.status]}
          </span>
        </Prop>
        <Prop k="Revision">
          <span className="mono">{a.latest_revision ? a.latest_revision.revision_number : "—"}</span>
        </Prop>
        {a.latest_revision && (
          <Prop k="Completed">
            <Time iso={a.latest_revision.created_at} />
          </Prop>
        )}
      </section>

      <section aria-label="Grouped signals" className="side-section">
        <h2 className="side-title">
          Grouped signals <span className="mono muted">{signals.length}</span>
        </h2>
        {signals.map((s) => (
          <div key={s.event_id} className="prop prop-narrow">
            <Time iso={s.occurred_at} zone={false} />
            <span className={s.link_reason === "trigger" ? "" : "muted-2"}>
              {s.link_reason === "trigger" ? "Alert fired · opened incident" : "Repeat alert · joined incident"}
            </span>
          </div>
        ))}
      </section>
    </aside>
  );
}
