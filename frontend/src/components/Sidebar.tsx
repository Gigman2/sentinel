import type { Theme } from "../theme";
import type { Project } from "../types";
import type { Route } from "../App";

function Logo() {
  return (
    <svg width="20" height="20" viewBox="0 0 20 20" aria-hidden="true" className="logo">
      <rect width="20" height="20" rx="5" fill="var(--primary)" />
      <path d="M4 11h3l2-5 3 9 2-4h2" fill="none" stroke="var(--primary-fg)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function Flame() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M8 1.8c.9 2.3 4.2 3.8 4.2 7.4A4.2 4.2 0 0 1 3.8 9.2c0-1.7.9-2.8 1.8-3.6.2 1.4 1 2.1 1.6 2.3C7 5.8 7 3.6 8 1.8Z"
        fill="none"
        stroke="var(--crit)"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function Plug() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M6 2v3M10 2v3M4 5h8v3a4 4 0 0 1-8 0Z M8 12v2.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function Sidebar({
  projects,
  projectId,
  onProject,
  route,
  openCount,
  theme,
  onTheme,
  onSignOut,
}: {
  projects: Project[];
  projectId: string | null;
  onProject: (id: string) => void;
  route: Route;
  openCount: string | null;
  theme: Theme;
  onTheme: (t: Theme) => void;
  onSignOut: () => void;
}) {
  const project = projects.find((p) => p.id === projectId);
  const onIncidents = route.name === "list" || route.name === "detail";
  return (
    <aside className="sidebar">
      <div className="project">
        <a href="#/" className="nav project-name" aria-label="Sentinel incidents">
          <Logo />
          <span className="grow">{project?.name ?? "Sentinel"}</span>
        </a>
        {projects.length > 1 && (
          <label className="project-select">
            <span className="visually-hidden">Project</span>
            <select value={projectId ?? ""} onChange={(e) => onProject(e.target.value)}>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      <nav className="nav-group" aria-label="Main">
        <span className="lbl nav-heading">Response</span>
        <a href="#/" className={`nav${onIncidents ? " nav-on" : ""}`} aria-current={onIncidents ? "page" : undefined}>
          <Flame />
          <span className="grow">Incidents</span>
          {openCount !== null && (
            <span className="mono nav-count" aria-label={`${openCount} open`}>
              {openCount}
            </span>
          )}
        </a>
        <a href="#/integrations" className={`nav${route.name === "integrations" ? " nav-on" : ""}`} aria-current={route.name === "integrations" ? "page" : undefined}>
          <Plug />
          <span className="grow">Integrations</span>
        </a>
      </nav>
      <div className="grow sidebar-spacer" />
      <div className="appearance">
        <span className="lbl">Appearance</span>
        <div className="seg" role="group" aria-label="Appearance">
          {(["light", "dark"] as Theme[]).map((t) => (
            <button key={t} type="button" className={`seg-btn${theme === t ? " seg-on" : ""}`} aria-pressed={theme === t} onClick={() => onTheme(t)}>
              {t === "light" ? "Light" : "Dark"}
            </button>
          ))}
        </div>
        <button type="button" className="ghost sign-out" onClick={onSignOut}>
          Sign out
        </button>
      </div>
    </aside>
  );
}
