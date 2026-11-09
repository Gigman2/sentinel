import type { IncidentStatus } from "../types";

/** Open: a red ring with a dot. Resolved: a green check. */
export function StatusIcon({ status }: { status: IncidentStatus }) {
  const open = status === "open";
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" role="img" aria-label={open ? "Open" : "Resolved"} className="status-icon">
      {open ? (
        <>
          <circle cx="7" cy="7" r="5.6" fill="none" stroke="var(--crit)" strokeWidth="1.5" />
          <circle cx="7" cy="7" r="2.3" fill="var(--crit)" />
        </>
      ) : (
        <>
          <circle cx="7" cy="7" r="6.3" fill="var(--ok)" />
          <path d="M4.3 7.2 6.2 9l3.5-3.8" fill="none" stroke="var(--surface)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        </>
      )}
    </svg>
  );
}
