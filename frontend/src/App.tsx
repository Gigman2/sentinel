import { useCallback, useEffect, useState, type ReactNode } from "react";
import { ApiError, apiFetch, describeError, getToken, onUnauthorized, setToken } from "./api";
import { SearchDialog } from "./components/SearchDialog";
import { Sidebar } from "./components/Sidebar";
import { parseFilters, type ListFilters } from "./filters";
import { applyTheme, initialTheme, type Theme } from "./theme";
import type { IncidentPage, Project } from "./types";
import { Alerts } from "./views/Alerts";
import { IncidentDetail } from "./views/IncidentDetail";
import { IncidentList } from "./views/IncidentList";
import { Integrations } from "./views/Integrations";
import { SignIn } from "./views/SignIn";

export type Route =
  | { name: "list"; filters: ListFilters }
  | { name: "detail"; id: string }
  | { name: "integrations" }
  | { name: "alerts" };

const PROJECT_KEY = "sentinel.project";

export function parseRoute(hash: string): Route {
  const [path, query = ""] = hash.replace(/^#/, "").split("?", 2);
  const m = path.match(/^\/incidents\/([^/]+)$/);
  if (m) return { name: "detail", id: decodeURIComponent(m[1]) };
  if (path === "/integrations") return { name: "integrations" };
  if (path === "/alerts") return { name: "alerts" };
  return { name: "list", filters: parseFilters(query) };
}

function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash));
  useEffect(() => {
    const on = () => setRoute(parseRoute(window.location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

function savedProject(): string | null {
  try {
    return sessionStorage.getItem(PROJECT_KEY);
  } catch {
    return null;
  }
}

export default function App() {
  const route = useRoute();
  const [signedIn, setSignedIn] = useState(() => getToken() !== null);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectError, setProjectError] = useState<string | null>(null);
  const [projectId, setProjectId] = useState<string | null>(savedProject);
  const [openCount, setOpenCount] = useState<number | null>(null);
  const [searching, setSearching] = useState(false);
  const [theme, setTheme] = useState<Theme>(initialTheme);

  useEffect(() => applyTheme(theme, false), [theme]);

  // Any 401 sends the person back to sign-in.
  useEffect(() => {
    onUnauthorized(() => {
      setSignedIn(false);
      setProjects(null);
    });
    return () => onUnauthorized(() => {});
  }, []);

  useEffect(() => {
    if (!signedIn || projects) return;
    let active = true;
    apiFetch<{ items: Project[] }>("GET", "/v1/projects")
      .then((r) => active && setProjects(r.items))
      .catch((e) => {
        if (active && !(e instanceof ApiError && e.status === 401)) setProjectError(describeError(e));
      });
    return () => {
      active = false;
    };
  }, [signedIn, projects]);

  // Keep the chosen project if it still exists, otherwise use the first.
  const current = projects?.find((p) => p.id === projectId)?.id ?? projects?.[0]?.id ?? null;
  const chooseProject = useCallback((id: string) => {
    setProjectId(id);
    setOpenCount(null);
    try {
      sessionStorage.setItem(PROJECT_KEY, id);
    } catch {
      // Not remembered.
    }
  }, []);

  // The sidebar's open count, when the list (which reports it) isn't showing.
  const onList = route.name === "list";
  useEffect(() => {
    if (!signedIn || !current || onList || openCount !== null) return;
    let active = true;
    apiFetch<IncidentPage>("GET", `/v1/projects/${current}/incidents?status=open&limit=1`)
      .then((p) => active && setOpenCount(p.open_count))
      .catch(() => {
        // The count stays hidden.
      });
    return () => {
      active = false;
    };
  }, [signedIn, current, onList, openCount]);

  // ⌘K / Ctrl-K opens search.
  useEffect(() => {
    if (!signedIn || !current) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSearching(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [signedIn, current]);
  const closeSearch = useCallback(() => setSearching(false), []);

  if (!signedIn) {
    return (
      <SignIn
        onSignedIn={(items) => {
          setProjects(items);
          setProjectError(null);
          setSignedIn(true);
        }}
      />
    );
  }

  let page: ReactNode;
  if (route.name === "detail") {
    page = <IncidentDetail key={route.id} id={route.id} />;
  } else if (projectError) {
    page = <div className="page-note error-text">{projectError}</div>;
  } else if (!projects) {
    page = <div className="page-note">Loading…</div>;
  } else if (!current) {
    page = <div className="page-note">This organisation has no projects yet.</div>;
  } else if (route.name === "integrations") {
    page = <Integrations key={current} projectId={current} />;
  } else if (route.name === "alerts") {
    page = <Alerts key={current} projectId={current} />;
  } else {
    page = <IncidentList key={current} projectId={current} filters={route.filters} onOpenCount={setOpenCount} />;
  }

  return (
    <div className="app">
      <Sidebar
        projects={projects ?? []}
        projectId={current}
        onProject={chooseProject}
        route={route}
        openCount={openCount}
        onSearch={current ? () => setSearching(true) : undefined}
        theme={theme}
        onTheme={(t) => {
          setTheme(t);
          applyTheme(t, true);
        }}
        onSignOut={() => {
          setToken(null);
          setSignedIn(false);
          setProjects(null);
        }}
      />
      <main className="main">
        <div className="sheet">{page}</div>
      </main>
      {searching && current && <SearchDialog projectId={current} onClose={closeSearch} />}
    </div>
  );
}
