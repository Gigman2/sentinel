import { useCallback, useEffect, useState, type ReactNode } from "react";
import { ApiError, apiFetch, describeError, getToken, onUnauthorized, setToken } from "./api";
import { Sidebar } from "./components/Sidebar";
import { applyTheme, initialTheme, type Theme } from "./theme";
import type { Project } from "./types";
import { IncidentDetail } from "./views/IncidentDetail";
import { IncidentList } from "./views/IncidentList";
import { Integrations } from "./views/Integrations";
import { SignIn } from "./views/SignIn";

export type Route = { name: "list" } | { name: "detail"; id: string } | { name: "integrations" };

const PROJECT_KEY = "sentinel.project";

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, "");
  const m = path.match(/^\/incidents\/([^/?#]+)$/);
  if (m) return { name: "detail", id: decodeURIComponent(m[1]) };
  if (path === "/integrations") return { name: "integrations" };
  return { name: "list" };
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
  const [openCount, setOpenCount] = useState<string | null>(null);
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
  } else {
    page = <IncidentList key={current} projectId={current} onOpenCount={setOpenCount} />;
  }

  return (
    <div className="app">
      <Sidebar
        projects={projects ?? []}
        projectId={current}
        onProject={chooseProject}
        route={route}
        openCount={openCount}
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
    </div>
  );
}
