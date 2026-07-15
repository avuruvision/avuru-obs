"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";
import { cn } from "@/lib/cn";

const INPUT =
  "h-9 w-full rounded-lg border border-neutral bg-base-100 px-3 text-sm outline-none placeholder:text-base-content/40 focus:border-primary";

// Free-text filter with type-ahead suggestions — an editable input plus an
// absolutely-positioned listbox of matching options (same primitives as Select,
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
  const ref = useRef<HTMLDivElement>(null);
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

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
        setText(value); // discard uncommitted typing
      }
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open, value]);

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
      if (!open) setOpen(true);
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
    <div ref={ref} className={cn("relative", className)}>
      <input
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
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
        className={INPUT}
      />
      {open && (matches.length > 0 || loading) && (
        <ul
          role="listbox"
          id={listId}
          aria-label={ariaLabel}
          className="absolute z-30 mt-1 max-h-60 w-full overflow-auto rounded-lg border border-neutral bg-base-100 py-1 [box-shadow:var(--shadow-card-hover)]"
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
        </ul>
      )}
    </div>
  );
}
