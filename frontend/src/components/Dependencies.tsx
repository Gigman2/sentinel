import type { DownstreamComponent, Incident } from "../types";

/** The registered components this incident's component calls, two hops at
 * most. They are registered by people; Sentinel gathers no evidence from
 * them yet, so they are not called "affected". */
export function Dependencies({ incident }: { incident: Incident }) {
  const deps = incident.dependencies;
  const direct = deps.filter((d) => d.depth === 1);
  const under = (id: string) => deps.filter((d) => d.depth === 2 && d.via_component_id === id);

  const row = (d: DownstreamComponent) => (
    <li key={d.component.id} className={`dep dep-${d.depth}`}>
      <span aria-hidden="true" className="dep-arrow">
        ↳
      </span>
      {d.component.kind === "external" ? <span>{d.component.display_name}</span> : <span className="mono small">{d.component.slug}</span>}
      <span className="dep-meta">{d.component.kind === "external" ? "external" : "calls"}</span>
    </li>
  );

  return (
    <section aria-labelledby="deps-h" className="side-section">
      <h2 id="deps-h" className="side-title">
        Registered dependencies
      </h2>
      <ul className="deps">
        <li className="dep">
          <span className="mono small">{incident.component.slug}</span>
          <span className="dep-meta">primary</span>
        </li>
        {direct.flatMap((d) => [row(d), ...under(d.component.id).map(row)])}
      </ul>
      {deps.length === 0 && <span className="muted small">No dependencies are registered for {incident.component.slug}.</span>}
      <span className="muted small deps-note">No evidence is gathered from dependencies yet.</span>
    </section>
  );
}
