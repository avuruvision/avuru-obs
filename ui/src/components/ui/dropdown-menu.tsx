"use client";

import { useCallback, useState } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/cn";
import { useAnchoredLayer } from "@/hooks/use-anchored-layer";

export interface DropdownMenuItem {
  label: string;
  onSelect: () => void;
}

// Minimal action menu — same no-dependency recipe as Select (field-styled
// trigger + portalled panel), but menu semantics: items run an action instead
// of holding a selection.
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
  const dismiss = useCallback(() => setOpen(false), []);
  const { anchorRef, layerRef, style, reposition } = useAnchoredLayer<
    HTMLButtonElement,
    HTMLDivElement
  >({ open, onDismiss: dismiss, align, width: "auto" });

  const show = () => {
    setActive(0);
    reposition();
    setOpen(true);
  };

  const run = (item: DropdownMenuItem) => {
    setOpen(false);
    item.onSelect();
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      setOpen(false);
    } else if (!open && (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      show();
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
    <div className="relative inline-flex" onKeyDown={onKeyDown}>
      <button
        ref={anchorRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={() => (open ? setOpen(false) : show())}
        className={triggerClassName}
      >
        {trigger}
      </button>
      {open &&
        style &&
        createPortal(
          <div
            ref={layerRef}
            role="menu"
            aria-label={ariaLabel}
            style={style}
            className="z-50 min-w-44 overflow-auto rounded-lg border border-neutral bg-base-100 py-1 [box-shadow:var(--shadow-card-hover)]"
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
          </div>,
          document.body,
        )}
    </div>
  );
}
