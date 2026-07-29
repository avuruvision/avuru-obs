"use client";

import { useEffect, useRef, useState } from "react";
import { Check, ChevronsUpDown, FolderKanban } from "lucide-react";
import { cn } from "@/lib/cn";
import { useProject } from "@/lib/project-context";
import { useProjects } from "@/hooks/use-projects";

// Coroot-style project switcher, pinned in the sidebar footer. The listbox
// opens UPWARD (footer placement) — the shared Select opens down, hence the
// dedicated primitive with the same styling.
export function ProjectSwitcher({ collapsed }: { collapsed: boolean }) {
  const { project, setProject } = useProject();
  const { data } = useProjects();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // Before the list loads (or with the hub down) the active project still
  // renders — switching just isn't possible yet.
  const projects = data?.projects ?? [{ id: project, source: "default" as const }];
  // The footer shows the active project's label when it has one (db projects),
  // falling back to the id; selection still keys on the id (the tenant).
  const activeLabel = projects.find((p) => p.id === project)?.label || project;

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label="Switch project"
        aria-haspopup="listbox"
        aria-expanded={open}
        title={collapsed ? `Project: ${project}` : undefined}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={(e) => {
          if (e.key === "Escape") setOpen(false);
        }}
        className={cn(
          "flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left transition-colors hover:bg-base-300",
          collapsed && "justify-center px-0",
        )}
      >
        <FolderKanban className="h-4 w-4 shrink-0 text-base-content/50" aria-hidden />
        {!collapsed && (
          <>
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="text-[10px] uppercase tracking-wider text-base-content/40">
                Project
              </span>
              <span className="truncate text-sm font-medium">{activeLabel}</span>
            </span>
            <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-base-content/50" aria-hidden />
          </>
        )}
      </button>
      {open && (
        <ul
          role="listbox"
          aria-label="Projects"
          className="absolute bottom-full left-0 z-30 mb-1 max-h-60 w-44 overflow-auto rounded-lg border border-neutral bg-base-100 py-1 [box-shadow:var(--shadow-card-hover)]"
        >
          {projects.map((p) => {
            const isSel = p.id === project;
            return (
              <li
                key={p.id}
                role="option"
                aria-selected={isSel}
                onClick={() => {
                  setProject(p.id);
                  setOpen(false);
                }}
                className={cn(
                  "flex cursor-pointer items-center justify-between gap-2 px-3 py-1.5 text-sm hover:bg-base-300",
                  isSel ? "text-primary" : "text-base-content",
                )}
              >
                <span className="truncate">{p.label || p.id}</span>
                {isSel && <Check className="h-3.5 w-3.5 shrink-0" />}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
