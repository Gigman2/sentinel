import { useCallback, useEffect, useState } from "react";
import { ApiError, apiFetch, describeError } from "../api";
import { Dot } from "../components/Dot";
import { SeverityChip } from "../components/SeverityChip";
import { StatusIcon } from "../components/StatusIcon";
import { relative, shortId, whenSeen } from "../format";
import { ANALYSIS_LABEL, ANALYSIS_TONE } from "../labels";
import type { IncidentListItem, Page } from "../types";

type StatusFilter = "all" | "open" | "resolved";

const FILTERS: [StatusFilter, string][] = [
  ["all", "All"],
  ["open", "Open"],
  ["resolved", "Resolved"],
];

/** Incidents in the project, newest first, grouped into open and resolved. */
export function IncidentList({ projectId, onOpenCount }: { projectId: string; onOpenCount: (count: string | null) => void }) {
  const [filter, setFilter] = useState<StatusFilter>("all");
  const [items, setItems] = useState<IncidentListItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "more" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  const query = useCallback(
    (after: string | null) => {
      const q = new URLSearchParams({ limit: "50" });
      if (filter !== "all") q.set("status", filter);
      if (after) q.set("cursor", after);
      return `/v1/projects/${projectId}/incidents?${q}`;
    },
    [projectId, filter],
  );

  useEffect(() => {
    let active = true;
    setState("loading");
    setItems([]);
    setCursor(null);
    apiFetch<Page<IncidentListItem>>("GET", query(null))
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
  }, [query, attempt]);

  // Until the API reports a project-wide open count (Task 12), count the
  // open incidents that are loaded; "+" means more pages exist.
  const open = items.filter((i) => i.status === "open").length;
  useEffect(() => {
    if (state !== "ready" || filter === "resolved") return;
    onOpenCount(`${open}${cursor ? "+" : ""}`);
  }, [state, filter, open, cursor, onOpenCount]);

  async function loadMore() {
    if (!cursor) return;
    setState("more");
    try {
      const page = await apiFetch<Page<IncidentListItem>>("GET", query(cursor));
      setItems((prev) => [...prev, ...page.items]);
      setCursor(page.next_cursor);
      setState("ready");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return;
      setError(describeError(e));
      setState("error");
    }
  }

  const showAnalysis = items.some((i) => i.analysis_status !== undefined);
  const showSignals = items.some((i) => i.signal_count !== undefined);
  const cols = ["16px", "76px", "minmax(160px,1fr)", "108px", showAnalysis && "150px", showSignals && "92px", "96px"].filter(Boolean).join(" ");
  const groups = (["open", "resolved"] as const)
    .map((status) => ({ status, label: status === "open" ? "Open" : "Resolved", rows: items.filter((i) => i.status === status) }))
    .filter((g) => g.rows.length > 0);
  const now = new Date();

  return (
    <>
      <header className="page-head">
        <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
          <path
            d="M8 1.8c.9 2.3 4.2 3.8 4.2 7.4A4.2 4.2 0 0 1 3.8 9.2c0-1.7.9-2.8 1.8-3.6.2 1.4 1 2.1 1.6 2.3C7 5.8 7 3.6 8 1.8Z"
            fill="none"
            stroke="var(--crit)"
            strokeWidth="1.4"
            strokeLinejoin="round"
          />
        </svg>
        <h1>Incidents</h1>
        {state === "ready" && (
          <span className="muted">
            {items.length}
            {cursor ? "+" : ""} shown · {open} open
          </span>
        )}
        <span className="grow" />
        <span className="mono muted small">Times in UTC</span>
      </header>

      <div className="filters">
        <div className="seg" role="group" aria-label="Status">
          {FILTERS.map(([k, label]) => (
            <button key={k} type="button" className={`seg-btn${filter === k ? " seg-on" : ""}`} aria-pressed={filter === k} onClick={() => setFilter(k)}>
              {label}
            </button>
          ))}
        </div>
      </div>

      <div className="table-scroll">
        <div role="table" aria-label="Incidents" className="itable" style={{ ["--cols" as string]: cols }}>
          <div role="rowgroup">
            <div role="row" className="ihead">
              <span role="columnheader" aria-label="Status" />
              <span role="columnheader">ID</span>
              <span role="columnheader">Incident</span>
              <span role="columnheader">Severity</span>
              {showAnalysis && <span role="columnheader">Analysis</span>}
              {showSignals && <span role="columnheader">Signals</span>}
              <span role="columnheader" className="right">
                Last seen
              </span>
            </div>
          </div>

          {state === "loading" && <div className="table-note">Loading incidents…</div>}
          {state === "error" && (
            <div className="table-note">
              <span className="error-text">{error}</span>{" "}
              <button type="button" className="ghost" onClick={() => setAttempt((n) => n + 1)}>
                Try again
              </button>
            </div>
          )}
          {state !== "loading" && state !== "error" && items.length === 0 && <div className="table-note">No incidents match these filters.</div>}

          {groups.map((g) => (
            <div role="rowgroup" aria-label={g.label} key={g.status}>
              <div role="row" className="igroup">
                <span role="cell" className="igroup-cell">
                  <Dot color={g.status === "open" ? "var(--crit)" : "var(--ok)"} />
                  <span className="igroup-label">{g.label}</span>
                  <span className="mono muted small">{g.rows.length}</span>
                </span>
              </div>
              {g.rows.map((r) => {
                const href = `#/incidents/${r.id}`;
                return (
                  <div role="row" key={r.id} className="irow" onClick={() => (window.location.hash = href)}>
                    <span role="cell">
                      <StatusIcon status={r.status} />
                    </span>
                    <span role="cell" className="mono muted small">
                      {shortId(r.id, r.number)}
                    </span>
                    <span role="cell" className="icell-title">
                      <a href={href} className="ititle" onClick={(e) => e.stopPropagation()}>
                        {r.title}
                      </a>
                      <span className="isub">
                        <span className="mono">{r.component.slug}</span> · {r.environment}
                        {r.resolution_note ? ` · Cause (recorded): ${r.resolution_note}` : ""}
                      </span>
                    </span>
                    <span role="cell">
                      <SeverityChip severity={r.severity} muted={r.status !== "open"} />
                    </span>
                    {showAnalysis && (
                      <span role="cell" className="inline-dot muted-2">
                        {r.analysis_status && (
                          <>
                            <Dot color={ANALYSIS_TONE[r.analysis_status]} />
                            {ANALYSIS_LABEL[r.analysis_status]}
                          </>
                        )}
                      </span>
                    )}
                    {showSignals && (
                      <span role="cell" className="muted-2">
                        <span className="mono fg">{r.signal_count ?? "–"}</span> {(r.signal_count ?? 0) > 1 ? "grouped" : "signal"}
                      </span>
                    )}
                    <span role="cell" className="icell-time">
                      <span className="mono small" title={r.last_seen_at}>
                        {whenSeen(r.last_seen_at, now)}
                      </span>
                      <span className="muted small">{relative(r.last_seen_at, now)}</span>
                    </span>
                  </div>
                );
              })}
            </div>
          ))}
        </div>
      </div>

      {cursor && state !== "loading" && (
        <div className="load-more">
          <button type="button" className="btn" onClick={loadMore} disabled={state === "more"}>
            {state === "more" ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
      <p className="footnote">A repeated alert joins its open incident instead of creating a new row.</p>
    </>
  );
}
