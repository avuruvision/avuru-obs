"use client";

import { useCallback, useId, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";
import { cn } from "@/lib/cn";
import { useAnchoredLayer } from "@/hooks/use-anchored-layer";

export interface SelectOption {
  value: string;
  label: string;
  // Optional second line — room to say what a choice means without a tooltip.
  hint?: string;
}

// Themed dropdown — replaces native <select>, whose option list renders as an
// unstyled OS popup that clashes with the Avuru theme. Built from primitives (no
// dependency): a field-styled trigger + a portalled listbox anchored to it.
export function Select({
  value,
  options,
  onChange,
  ariaLabel,
  className,
  disabled,
}: {
  value: string;
  options: SelectOption[];
  onChange: (value: string) => void;
  ariaLabel?: string;
  className?: string;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const listId = useId();

  const dismiss = useCallback(() => setOpen(false), []);
  const { anchorRef, layerRef, style, reposition } = useAnchoredLayer<
    HTMLButtonElement,
    HTMLUListElement
  >({ open, onDismiss: dismiss, desiredHeight: Math.min(240, options.length * 34 + 8) });

  const selected = options.find((o) => o.value === value) ?? options[0];

  const openList = () => {
    setActive(Math.max(0, options.findIndex((o) => o.value === value)));
    reposition();
    setOpen(true);
  };

  const choose = (v: string) => {
    onChange(v);
    setOpen(false);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      setOpen(false);
    } else if (!open && (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      openList();
    } else if (open && e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => Math.min(options.length - 1, i + 1));
    } else if (open && e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(0, i - 1));
    } else if (open && (e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      choose(options[active]?.value ?? value);
    }
  };

  return (
    <div className={cn("relative h-9", className)}>
      <button
        ref={anchorRef}
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-label={ariaLabel}
        disabled={disabled}
        onClick={() => (open ? setOpen(false) : openList())}
        onKeyDown={onKeyDown}
        className="flex h-full w-full items-center justify-between gap-2 rounded-lg border border-neutral bg-base-100 px-3 text-sm outline-none focus:border-primary disabled:cursor-not-allowed disabled:opacity-50"
      >
        <span className="truncate">{selected?.label}</span>
        <ChevronDown
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-base-content/50 transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open &&
        style &&
        createPortal(
          <ul
            ref={layerRef}
            role="listbox"
            id={listId}
            aria-label={ariaLabel}
            style={style}
            className="z-50 overflow-auto rounded-lg border border-neutral bg-base-100 py-1 [box-shadow:var(--shadow-card-hover)]"
          >
            {options.map((o, i) => {
              const isSel = o.value === value;
              return (
                <li
                  key={o.value}
                  role="option"
                  aria-selected={isSel}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => choose(o.value)}
                  className={cn(
                    "flex cursor-pointer items-center justify-between gap-2 px-3 py-1.5 text-sm",
                    i === active && "bg-base-300",
                    isSel ? "text-primary" : "text-base-content",
                  )}
                >
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate">{o.label}</span>
                    {o.hint && (
                      <span className="truncate text-xs text-base-content/50">{o.hint}</span>
                    )}
                  </span>
                  {isSel && <Check className="h-3.5 w-3.5 shrink-0" />}
                </li>
              );
            })}
          </ul>,
          document.body,
        )}
    </div>
  );
}
