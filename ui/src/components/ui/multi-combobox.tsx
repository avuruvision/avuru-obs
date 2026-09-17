"use client";

import { useCallback, useId, useMemo, useState } from "react";
import { Check, Plus, X } from "lucide-react";
import { createPortal } from "react-dom";
import { useAnchoredLayer } from "@/hooks/use-anchored-layer";
import { cn } from "@/lib/cn";

export interface MultiComboboxOption {
  value: string;
  label: string;
  description?: string;
}

export function MultiCombobox({
  selected,
  options,
  onChange,
  ariaLabel,
  placeholder,
  allowCustom,
  className,
}: {
  selected: string[];
  options: MultiComboboxOption[];
  onChange: (values: string[]) => void;
  ariaLabel: string;
  placeholder: string;
  allowCustom?: (text: string) => MultiComboboxOption;
  className?: string;
}) {
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const listId = useId();
  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const matches = useMemo(() => {
    const needle = text.trim().toLowerCase();
    return options
      .filter((option) => !needle || `${option.label} ${option.description ?? ""}`.toLowerCase().includes(needle))
      .slice(0, 50);
  }, [options, text]);

  const dismiss = useCallback(() => setOpen(false), []);
  const { anchorRef, layerRef, style, reposition } = useAnchoredLayer<
    HTMLInputElement,
    HTMLUListElement
  >({ open, onDismiss: dismiss });
  const show = () => {
    reposition();
    setOpen(true);
  };
  const toggle = (value: string) => {
    onChange(selectedSet.has(value) ? selected.filter((item) => item !== value) : [...selected, value]);
    setText("");
    setActive(0);
  };
  const commitCustom = () => {
    const value = text.trim();
    if (!value || !allowCustom) return;
    const option = allowCustom(value);
    if (!selectedSet.has(option.value)) onChange([...selected, option.value]);
    setText("");
  };

  return (
    <div className={cn("flex min-w-64 flex-col gap-1.5", className)}>
      <input
        ref={anchorRef}
        role="combobox"
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        value={text}
        placeholder={placeholder}
        onFocus={show}
        onChange={(event) => {
          setText(event.target.value);
          setActive(0);
          show();
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") dismiss();
          if (event.key === "ArrowDown") {
            event.preventDefault();
            show();
            setActive((index) => Math.min(matches.length - 1, index + 1));
          }
          if (event.key === "ArrowUp") {
            event.preventDefault();
            setActive((index) => Math.max(0, index - 1));
          }
          if (event.key === "Enter") {
            event.preventDefault();
            if (open && matches[active]) toggle(matches[active].value);
            else commitCustom();
          }
        }}
        className="h-9 w-full rounded-lg border border-neutral bg-base-200 px-3 text-sm outline-none placeholder:text-base-content/40 focus:border-primary"
      />
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1.5" aria-label="Selected log services">
          {selected.map((value) => {
            const option = options.find((item) => item.value === value) ?? { value, label: value.replace(/^[^:]+:/, "") };
            return (
              <span key={value} className="inline-flex items-center gap-1 rounded-md bg-primary/15 px-2 py-1 text-xs text-primary">
                <span>{option.label}</span>
                {option.description && <span className="text-base-content/50">{option.description}</span>}
                <button type="button" aria-label={`Remove ${option.label}`} onClick={() => toggle(value)}>
                  <X className="h-3 w-3" aria-hidden />
                </button>
              </span>
            );
          })}
        </div>
      )}
      {open && style && createPortal(
        <ul
          ref={layerRef}
          id={listId}
          role="listbox"
          aria-multiselectable="true"
          aria-label={ariaLabel}
          style={style}
          className="z-50 max-h-72 overflow-auto rounded-lg border border-neutral bg-base-100 py-1 [box-shadow:var(--shadow-card-hover)]"
        >
          {matches.map((option, index) => (
            <li
              key={option.value}
              role="option"
              aria-selected={selectedSet.has(option.value)}
              onMouseEnter={() => setActive(index)}
              onMouseDown={(event) => {
                event.preventDefault();
                toggle(option.value);
              }}
              className={cn("flex cursor-pointer items-center gap-2 px-3 py-2 text-sm", index === active && "bg-base-300")}
            >
              <span className="flex h-4 w-4 items-center justify-center rounded border border-neutral">
                {selectedSet.has(option.value) && <Check className="h-3 w-3" aria-hidden />}
              </span>
              <span className="min-w-0 flex-1 truncate">{option.label}</span>
              {option.description && <span className="text-xs text-base-content/50">{option.description}</span>}
            </li>
          ))}
          {text.trim() && allowCustom && !matches.some((option) => option.label === text.trim()) && (
            <li
              role="option"
              aria-selected="false"
              onMouseDown={(event) => { event.preventDefault(); commitCustom(); }}
              className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm text-primary hover:bg-base-300"
            >
              <Plus className="h-4 w-4" aria-hidden /> Use exact service “{text.trim()}”
            </li>
          )}
        </ul>,
        document.body,
      )}
    </div>
  );
}
