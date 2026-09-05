"use client";

import { useCallback, useId, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/cn";
import { useAnchoredLayer } from "@/hooks/use-anchored-layer";

const INPUT =
  "h-9 w-full rounded-lg border border-neutral bg-base-100 px-3 text-sm outline-none placeholder:text-base-content/40 focus:border-primary";

// Free-text filter with type-ahead suggestions — an editable input plus a
// portalled listbox of matching options (same primitives as Select,
// no dependency). Because it's a filter, arbitrary text is allowed: it commits on
// Enter (the highlighted option, or the raw text) and on option click. Blurring or
// pressing Escape without committing reverts to the last applied value.
export function Combobox({
  value,
  options,
  onCommit,
  placeholder,
  ariaLabel,
  loading,
  className,
}: {
  value: string;
  options: string[];
  onCommit: (value: string) => void;
  placeholder?: string;
  ariaLabel?: string;
  loading?: boolean;
  className?: string;
}) {
  const [text, setText] = useState(value);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const listId = useId();

  // Reflect external changes to the applied value (e.g. the Clear button, or a
  // service picked from the Overview tab). React's "adjust state during render
  // when a prop changes" pattern — no effect, no cascading render.
  const [lastValue, setLastValue] = useState(value);
  if (value !== lastValue) {
    setLastValue(value);
    setText(value);
  }

  const matches = useMemo(() => {
    const q = text.trim().toLowerCase();
    const list = q ? options.filter((o) => o.toLowerCase().includes(q)) : options;
    return list.slice(0, 50);
  }, [options, text]);

  const dismiss = useCallback(() => {
    setOpen(false);
    setText(value); // discard uncommitted typing
  }, [value]);
  const { anchorRef, layerRef, style, reposition } = useAnchoredLayer<
    HTMLInputElement,
    HTMLUListElement
  >({ open, onDismiss: dismiss });

  const show = () => {
    reposition();
    setOpen(true);
  };

  const commit = (v: string) => {
    const next = v.trim();
    onCommit(next);
    setText(next);
    setOpen(false);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      setOpen(false);
      setText(value);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      if (!open) show();
      else setActive((i) => Math.min(matches.length - 1, i + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(0, i - 1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      commit(open && matches[active] ? matches[active] : text);
    }
  };

  return (
    <div className={cn("relative", className)}>
      <input
        ref={anchorRef}
        type="text"
        role="combobox"
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-label={ariaLabel}
        value={text}
        placeholder={placeholder}
        onChange={(e) => {
          setText(e.target.value);
          setActive(0);
          show();
        }}
        onFocus={show}
        onKeyDown={onKeyDown}
        className={INPUT}
      />
      {open &&
        style &&
        (matches.length > 0 || loading) &&
        createPortal(
          <ul
            ref={layerRef}
            role="listbox"
            id={listId}
            aria-label={ariaLabel}
            style={style}
            className="z-50 overflow-auto rounded-lg border border-neutral bg-base-100 py-1 [box-shadow:var(--shadow-card-hover)]"
          >
            {loading && matches.length === 0 && (
              <li className="px-3 py-1.5 text-sm text-base-content/50">Loading…</li>
            )}
            {matches.map((o, i) => (
              <li
                key={o}
                role="option"
                aria-selected={o === value}
                onMouseEnter={() => setActive(i)}
                // mousedown, not click: fire before the input's blur reverts the text
                onMouseDown={(e) => {
                  e.preventDefault();
                  commit(o);
                }}
                className={cn(
                  "cursor-pointer truncate px-3 py-1.5 text-sm",
                  i === active && "bg-base-300",
                  o === value ? "text-primary" : "text-base-content",
                )}
              >
                {o}
              </li>
            ))}
          </ul>,
          document.body,
        )}
    </div>
  );
}
