import { useEffect, useState } from "react";
import { ApiError, apiFetch, describeError } from "../api";
import { Dot } from "../components/Dot";
import { utcTime } from "../format";
import { CAPABILITY_LABEL } from "../labels";
import type { Integration } from "../types";

/** The project's integrations, read-only. */
export function Integrations({ projectId }: { projectId: string }) {
  const [items, setItems] = useState<Integration[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    apiFetch<{ items: Integration[] }>("GET", `/v1/projects/${projectId}/integrations`)
      .then((r) => active && setItems(r.items))
      .catch((e) => {
        if (active && !(e instanceof ApiError && e.status === 401)) setError(describeError(e));
      });
    return () => {
      active = false;
    };
  }, [projectId]);

  return (
    <>
      <header className="page-head">
        <h1>Integrations</h1>
        <span className="muted">Where Sentinel's events come from</span>
      </header>
      <div className="table-scroll">
        <table className="ptable">
          <thead>
            <tr>
              <th>Name</th>
              <th>Capability</th>
              <th>Status</th>
              <th>Component mappings</th>
              <th className="right">Created</th>
            </tr>
          </thead>
          <tbody>
            {error && (
              <tr>
                <td colSpan={5} className="error-text">
                  {error}
                </td>
              </tr>
            )}
            {!items && !error && (
              <tr>
                <td colSpan={5} className="muted">
                  Loading integrations…
                </td>
              </tr>
            )}
            {items?.length === 0 && (
              <tr>
                <td colSpan={5} className="muted">
                  No integrations in this project yet.
                </td>
              </tr>
            )}
            {items?.map((it) => (
              <tr key={it.id}>
                <td>{it.name}</td>
                <td>{CAPABILITY_LABEL[it.capability] ?? it.capability}</td>
                <td>
                  <span className="inline-dot">
                    <Dot color={it.enabled ? "var(--ok)" : "var(--fg3)"} />
                    {it.enabled ? "Enabled" : "Disabled"}
                  </span>
                </td>
                <td>
                  {it.mappings.length === 0 ? (
                    <span className="muted">None</span>
                  ) : (
                    it.mappings.map((m) => (
                      <div key={m.id} className="mono small">
                        {m.external_component_id} · {m.environment}
                      </div>
                    ))
                  )}
                </td>
                <td className="right mono small" title={it.created_at}>
                  {new Date(it.created_at).toISOString().slice(0, 10)} {utcTime(it.created_at).text}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
