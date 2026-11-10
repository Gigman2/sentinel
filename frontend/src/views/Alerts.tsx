import { useEffect, useState } from "react";
import { ApiError, apiFetch, describeError } from "../api";
import { SeverityChip } from "../components/SeverityChip";
import { safeHref, shortId, utcTime, whenSeen } from "../format";
import { DISPOSITION_LABEL } from "../labels";
import type { Page, Signal } from "../types";

const PAGE_SIZE = 50;

/** Every alert received for the project, newest first, with the incident
 * grouping put it in. */
export function Alerts({ projectId }: { projectId: string }) {
  const [items, setItems] = useState<Signal[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "more" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  const path = (after: string | null) => {
    const q = new URLSearchParams({ limit: String(PAGE_SIZE) });
    if (after) q.set("cursor", after);
    return `/v1/projects/${projectId}/signals?${q}`;
  };

  useEffect(() => {
    let active = true;
    setState("loading");
    apiFetch<Page<Signal>>("GET", path(null))
      .then((page) => {
        if (!active) return;
        setItems(page.items);
        setCursor(page.next_cursor);
        setState("ready");
      })
      .catch((e) => {
        if (!active || (e instanceof ApiError && e.status === 401)) return;
        setError(describeError(e));
        setState("error");
      });
    return () => {
      active = false;
    };
  }, [projectId, attempt]); // path() depends only on projectId

  async function loadMore() {
    if (!cursor) return;
    setState("more");
    try {
      const page = await apiFetch<Page<Signal>>("GET", path(cursor));
      setItems((prev) => [...prev, ...page.items]);
      setCursor(page.next_cursor);
      setState("ready");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return;
      setError(describeError(e));
      setState("error");
    }
  }

  const now = new Date();
  return (
    <>
      <header className="page-head">
        <h1>Alerts</h1>
        <span className="muted">Every alert received, and the incident it was grouped into</span>
        <span className="grow" />
        <span className="mono muted small">Times in UTC</span>
      </header>
      <div className="table-scroll">
        <table className="ptable" aria-label="Alerts">
          <thead>
            <tr>
              <th>Occurred</th>
              <th>Signal</th>
              <th>Alert</th>
              <th>Severity</th>
              <th>Incident</th>
            </tr>
          </thead>
          <tbody>
            {items.map((s) => {
              const t = utcTime(s.occurred_at, { seconds: true });
              const href = safeHref(s.source_url);
              const resolved = s.type === "alert.resolved";
              return (
                <tr key={s.event_id}>
                  <td className="mono small nowrap" title={t.title}>
                    {whenSeen(s.occurred_at, now)}
                  </td>
                  <td>
                    <span className="signal-type">
                      <span aria-hidden="true" style={{ color: resolved ? "var(--ok)" : "var(--crit)" }}>
                        {resolved ? "✓" : "◆"}
                      </span>
                      {resolved ? "Resolved" : "Triggered"}
                    </span>
                  </td>
                  <td>
                    <div>
                      {href ? (
                        <a href={href} target="_blank" rel="noopener noreferrer">
                          {s.summary || "Alert"}
                        </a>
                      ) : (
                        s.summary || <span className="muted">No summary</span>
                      )}
                    </div>
                    <div className="muted small">
                      <span className="mono">{s.component.slug}</span> · {s.environment}
                    </div>
                  </td>
                  <td>{s.severity ? <SeverityChip severity={s.severity} muted={resolved} /> : <span className="muted">–</span>}</td>
                  <td className="nowrap">
                    <span className="muted-2">{DISPOSITION_LABEL[s.disposition] ?? s.disposition}</span>
                    {s.incident && (
                      <>
                        {" "}
                        <a href={`#/incidents/${s.incident.id}`} className="mono">
                          {shortId(s.incident.id, s.incident.number)}
                        </a>
                      </>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {state === "loading" && <div className="table-note">Loading alerts…</div>}
        {state === "error" && (
          <div className="table-note">
            <span className="error-text">{error}</span>{" "}
            <button type="button" className="ghost" onClick={() => setAttempt((n) => n + 1)}>
              Try again
            </button>
          </div>
        )}
        {state === "ready" && items.length === 0 && <div className="table-note">No alerts have been received for this project yet.</div>}
      </div>
      {cursor && (state === "ready" || state === "more") && (
        <div className="load-more">
          <button type="button" className="btn" onClick={loadMore} disabled={state === "more"}>
            {state === "more" ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
    </>
  );
}
