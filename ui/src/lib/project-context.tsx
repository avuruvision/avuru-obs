"use client";

import {
  createContext,
  useContext,
  useEffect,
  useSyncExternalStore,
} from "react";
import { usePathname } from "next/navigation";

export const DEFAULT_PROJECT = "default";

const KEY = "avuru-project";
const CHANGE_EVENT = "avuru-project-change";

// The active project (= hub tenant). URL `?project=` is the shareable truth,
// localStorage bridges navigations that drop the param, `default` stays out
// of the URL so pre-project links keep their meaning. NO useSearchParams
// here: the provider wraps the sidebar, which is not under a Suspense
// boundary (static export) — window.location.search is ground truth, same
// doctrine as use-url-state.ts.
function subscribe(callback: () => void) {
  window.addEventListener("popstate", callback);
  window.addEventListener("storage", callback);
  window.addEventListener(CHANGE_EVENT, callback);
  return () => {
    window.removeEventListener("popstate", callback);
    window.removeEventListener("storage", callback);
    window.removeEventListener(CHANGE_EVENT, callback);
  };
}

function snapshot(): string {
  const fromURL = new URLSearchParams(window.location.search).get("project");
  if (fromURL) return fromURL;
  return localStorage.getItem(KEY) ?? DEFAULT_PROJECT;
}

const ProjectContext = createContext<{
  project: string;
  setProject: (p: string) => void;
}>({ project: DEFAULT_PROJECT, setProject: () => {} });

export function ProjectProvider({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const project = useSyncExternalStore(subscribe, snapshot, () => DEFAULT_PROJECT);

  // A shared link's ?project= must stick: mirror the resolved project into
  // localStorage so the next in-app navigation (which drops the query)
  // stays on the same project.
  useEffect(() => {
    if (localStorage.getItem(KEY) !== project) localStorage.setItem(KEY, project);
  }, [project]);

  // Re-materialize ?project= after navigations, keeping links shareable
  // (default is deliberately unmarked).
  useEffect(() => {
    if (project === DEFAULT_PROJECT) return;
    const params = new URLSearchParams(window.location.search);
    if (params.get("project") === project) return;
    params.set("project", project);
    window.history.replaceState(null, "", `${window.location.pathname}?${params.toString()}`);
  }, [pathname, project]);

  const setProject = (p: string) => {
    localStorage.setItem(KEY, p);
    const params = new URLSearchParams(window.location.search);
    if (p === DEFAULT_PROJECT) params.delete("project");
    else params.set("project", p);
    const qs = params.toString();
    window.history.replaceState(
      null,
      "",
      qs ? `${window.location.pathname}?${qs}` : window.location.pathname,
    );
    window.dispatchEvent(new Event(CHANGE_EVENT));
  };

  return (
    <ProjectContext.Provider value={{ project, setProject }}>
      {children}
    </ProjectContext.Provider>
  );
}

export function useProject() {
  return useContext(ProjectContext);
}
