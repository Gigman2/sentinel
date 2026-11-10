import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ApiError, apiFetch, describeError } from "../api";
import { AnalysisPanel } from "../components/AnalysisPanel";
import { Findings } from "../components/Findings";
import { Properties } from "../components/Properties";
import { ResolveDialog } from "../components/ResolveDialog";
import { StagePills } from "../components/StagePills";
import { Timeline } from "../components/Timeline";
import { shortId, utcTime } from "../format";
import type { Incident, Page, TimelineItem } from "../types";

const POLL_MS = 3000;

const busy = (inc: Incident | null) => inc?.analysis.status === "pending" || inc?.analysis.status === "running";

/** One incident: findings, timeline and properties. While its analysis is
 * pending or running the page re-reads it every 3 s. The parent mounts a
 * fresh instance per incident (key={id}), so a response for a previous
 * incident can never land here. */
export function IncidentDetail({ id }: { id: string }) {
  const [incident, setIncident] = useState<Incident | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  const [events, setEvents] = useState<TimelineItem[] | null>(null);
  const [timelineError, setTimelineError] = useState<string | null>(null);

  const [layout, setLayout] = useState<"grouped" | "stream">("grouped");
  const [highlight, setHighlight] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [resolving, setResolving] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState<string | null>(null);
  const noticeTimer = useRef<number>();

  // The incident, polled while analysis is in progress.
  useEffect(() => {
    let active = true;
    let timer: number | undefined;
    const load = async () => {
      try {
        const data = await apiFetch<Incident>("GET", `/v1/incidents/${id}`);
        if (!active) return;
        setIncident(data);
        setLoadError(null);
        if (busy(data)) timer = window.setTimeout(load, POLL_MS);
      } catch (e) {
        if (!active || (e instanceof ApiError && e.status === 401)) return;
        setLoadError(e instanceof ApiError && e.status === 404 ? "This incident doesn't exist, or you can't see it." : describeError(e));
      }
    };
    load();
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [id, reload]);

  // The timeline: every page, reloaded when a new revision lands.
  const revision = incident?.analysis.latest_revision?.revision_number ?? 0;
  const haveIncident = incident !== null;
  useEffect(() => {
    if (!haveIncident) return;
    let active = true;
    (async () => {
      try {
        const all: TimelineItem[] = [];
        let cursor: string | null = null;
        do {
          const q: string = cursor ? `?limit=200&cursor=${encodeURIComponent(cursor)}` : "?limit=200";
          const page: Page<TimelineItem> = await apiFetch("GET", `/v1/incidents/${id}/timeline${q}`);
          all.push(...page.items);
          cursor = page.next_cursor;
        } while (cursor && active);
        if (!active) return;
        setEvents(all);
        setTimelineError(null);
      } catch (e) {
        if (!active || (e instanceof ApiError && e.status === 401)) return;
        setTimelineError(describeError(e));
      }
    })();
    return () => {
      active = false;
    };
  }, [id, haveIncident, revision, reload]);

  const byId = useMemo(() => new Map((events ?? []).map((e) => [e.event_id, e])), [events]);

  const flash = useCallback((text: string) => {
    setNotice(text);
    window.clearTimeout(noticeTimer.current);
    noticeTimer.current = window.setTimeout(() => setNotice(null), 4000);
  }, []);
  useEffect(() => () => window.clearTimeout(noticeTimer.current), []);

  const cite = useCallback(
    (eventId: string) => {
      const row = document.getElementById(`evt-${eventId}`);
      if (!row) {
        flash("Event not on this page.");
        return;
      }
      row.scrollIntoView?.({ behavior: "smooth", block: "center" });
      setHighlight(eventId);
    },
    [flash],
  );

  async function retry() {
    setRetrying(true);
    setRetryError(null);
    try {
      await apiFetch("POST", `/v1/incidents/${id}/reanalyze`);
      setReload((n) => n + 1);
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 401)) setRetryError(describeError(e));
    } finally {
      setRetrying(false);
    }
  }

  async function share() {
    try {
      await navigator.clipboard.writeText(window.location.href);
      flash("Link copied.");
    } catch {
      flash("Couldn't copy the link; copy it from the address bar.");
    }
  }

  if (!incident) {
    return (
      <>
        <Breadcrumb label={shortId(id)} />
        <div className="page-note">{loadError ? <span className="error-text">{loadError}</span> : "Loading incident…"}</div>
      </>
    );
  }

  const label = shortId(incident.id, incident.number);
  const resolver = incident.resolved_by?.display_name;
  const rev = incident.analysis.latest_revision;
  const resolved = incident.status === "resolved";

  return (
    <>
      <Breadcrumb label={label}>
        {notice && (
          <span className="notice" role="status">
            {notice}
          </span>
        )}
        <button type="button" className="btn" onClick={share}>
          Share
        </button>
        {!resolved && (
          <button type="button" className="btn btn-dark" onClick={() => setResolving(true)}>
            Resolve incident
          </button>
        )}
      </Breadcrumb>

      <div className="detail">
        <div className="detail-main">
          <div className="detail-title">
            <h1>{incident.title}</h1>
            <StagePills incident={incident} />
          </div>

          {loadError && <div className="inline-error error-text">{loadError}</div>}

          {resolved && incident.resolution && (
            <div className="cause">
              {incident.resolution.note ? (
                <>
                  <span className="cause-head">
                    Cause · recorded at resolution · <span className="mono">{utcTime(incident.resolved_at).text} UTC</span>
                    {resolver && <> · Resolved by {resolver}</>}
                  </span>
                  <span className="cause-text">{incident.resolution.note}</span>
                </>
              ) : (
                <span className="cause-head">
                  {incident.resolution.source === "alert" ? "Resolved by its alert" : `Resolved by ${resolver ?? "hand"}`} at{" "}
                  <span className="mono">{utcTime(incident.resolved_at).text} UTC</span>. No cause was recorded.
                </span>
              )}
            </div>
          )}

          <section className="block" aria-labelledby="findings-h">
            <div className="block-head">
              <h2 id="findings-h">Findings</h2>
              {rev && <span className="mono muted small">{rev.findings.length}</span>}
              <span className="muted small">Only what the evidence supports. Nothing here is the cause.</span>
              <div className="seg seg-small" role="group" aria-label="Findings layout">
                {(
                  [
                    ["grouped", "By type"],
                    ["stream", "Stream"],
                  ] as const
                ).map(([k, l]) => (
                  <button key={k} type="button" className={`seg-btn${layout === k ? " seg-on" : ""}`} aria-pressed={layout === k} onClick={() => setLayout(k)}>
                    {l}
                  </button>
                ))}
              </div>
            </div>
            <AnalysisPanel analysis={incident.analysis} onRetry={retry} retrying={retrying} retryError={retryError} />
            {rev && (
              <>
                <Findings findings={rev.findings} events={byId} layout={layout} onCite={cite} />
                <span className="mono muted small revision-line" title={rev.created_at}>
                  Analysis revision {rev.revision_number} from {utcTime(rev.created_at, { seconds: true }).text}
                </span>
              </>
            )}
          </section>

          <section className="block" aria-labelledby="activity-h">
            <div className="block-head">
              <h2 id="activity-h">Activity</h2>
              {events && <span className="muted small">{events.length} events linked to this incident</span>}
            </div>
            {timelineError ? (
              <div className="inline-error">
                <span className="error-text">The timeline couldn't load: {timelineError}</span>{" "}
                <button type="button" className="ghost" onClick={() => setReload((n) => n + 1)}>
                  Try again
                </button>
              </div>
            ) : events ? (
              <Timeline items={events} highlight={highlight} />
            ) : (
              <span className="muted">Loading timeline…</span>
            )}
          </section>
        </div>

        <Properties incident={incident} events={events ?? []} onCite={cite} />
      </div>

      {resolving && (
        <ResolveDialog
          incidentId={incident.id}
          label={label}
          onClose={() => setResolving(false)}
          onResolved={(already) => {
            setResolving(false);
            if (already) flash("Already resolved by someone else.");
            setReload((n) => n + 1);
          }}
        />
      )}
    </>
  );
}

function Breadcrumb({ label, children }: { label: string; children?: ReactNode }) {
  return (
    <header className="page-head page-head-wrap">
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
        <path
          d="M8 1.8c.9 2.3 4.2 3.8 4.2 7.4A4.2 4.2 0 0 1 3.8 9.2c0-1.7.9-2.8 1.8-3.6.2 1.4 1 2.1 1.6 2.3C7 5.8 7 3.6 8 1.8Z"
          fill="none"
          stroke="var(--crit)"
          strokeWidth="1.4"
          strokeLinejoin="round"
        />
      </svg>
      <nav aria-label="Breadcrumb" className="crumbs">
        <a href="#/">Incidents</a>
        <span aria-hidden="true">›</span>
        <span className="mono" aria-current="page">
          {label}
        </span>
      </nav>
      <span className="grow" />
      {children}
    </header>
  );
}
