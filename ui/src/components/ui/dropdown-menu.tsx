"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";

export interface DropdownMenuItem {
  label: string;
  onSelect: () => void;
}

// Minimal action menu — same no-dependency recipe as Select (field-styled
// trigger + absolutely-positioned panel), but menu semantics: items run an
// action instead of holding a selection.
export function DropdownMenu({
  trigger,
  ariaLabel,
  items,
  align = "start",
  triggerClassName,
}: {
  trigger: React.ReactNode;
  ariaLabel: string;
  items: DropdownMenuItem[];
  align?: "start" | "end";
  triggerClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const run = (item: DropdownMenuItem) => {
    setOpen(false);
    item.onSelect();
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      setOpen(false);
    } else if (!open && (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      setActive(0);
      setOpen(true);
    } else if (open && e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => Math.min(items.length - 1, i + 1));
    } else if (open && e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(0, i - 1));
    } else if (open && (e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      const item = items[active];
      if (item) run(item);
    }
  };

  return (
    <div ref={ref} className="relative inline-flex" onKeyDown={onKeyDown}>
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={() => {
          setActive(0);
          setOpen((o) => !o);
        }}
        className={triggerClassName}
      >
        {trigger}
      </button>
      {open && (
        <div
          role="menu"
          aria-label={ariaLabel}
          className={cn(
            "absolute top-full z-30 mt-1 min-w-44 rounded-lg border border-neutral bg-base-100 py-1 [box-shadow:var(--shadow-card-hover)]",
            align === "end" ? "right-0" : "left-0",
          )}
        >
          {items.map((item, i) => (
            <button
              key={item.label}
              role="menuitem"
              onMouseEnter={() => setActive(i)}
              onClick={() => run(item)}
              className={cn(
                "block w-full px-3 py-1.5 text-left text-xs",
                i === active ? "bg-base-300" : "text-base-content",
              )}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
