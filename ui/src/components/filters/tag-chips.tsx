"use client";

import { X } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useTags } from "@/hooks/use-tags";
import type { TagKey } from "@/lib/api-types";

// Business-tag filters, offered rather than typed.
//
// The tag vocabulary is whatever an operator mapped in the chart — nobody can
// be expected to remember it, and a free-text field gives no feedback when a
// key is misspelled (it just returns nothing). So the discovered keys and a
// sample of their values are rendered as selects, and picking one edits the
// same `tags=` filter string the text field uses. One filter, two ways in.
//
// Nothing mapped means nothing rendered: an install with no tags gets no
// controls, not an empty one asking to be filled.

// parseTags reads the `key=value,key2=value2` filter string the API takes.
export function parseTags(raw: string | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const pair of (raw ?? "").split(",")) {
    const [k, ...rest] = pair.split("=");
    const key = k.trim();
    if (key && rest.length) out[key] = rest.join("=").trim();
  }
  return out;
}

// serializeTags is parseTags' inverse, with keys sorted so the URL — and
// therefore a shared link — does not depend on the order things were clicked.
export function serializeTags(tags: Record<string, string>): string | undefined {
  const keys = Object.keys(tags).sort();
  if (!keys.length) return undefined;
  return keys.map((k) => `${k}=${tags[k]}`).join(",");
}

export function TagChips({
  value,
  onChange,
  className,
}: {
  value: string | undefined;
  onChange: (next: string | undefined) => void;
  className?: string;
}) {
  const { time } = useTimeRange();
  const { data: available } = useTags(time);
  if (!available?.length) return null;

  const active = parseTags(value);
  const setTag = (tag: TagKey, next: string) => {
    const copy = { ...active };
    if (next) copy[tag.key] = next;
    else delete copy[tag.key];
    onChange(serializeTags(copy));
  };

  return (
    <div className={className} data-testid="tag-chips">
      <div className="flex flex-wrap items-center gap-1.5">
        {available.map((tag) => {
          const selected = active[tag.key] ?? "";
          return (
            <span
              key={tag.key}
              className={`flex items-center gap-1 rounded-full border py-0.5 pl-2.5 pr-1 text-xs ${
                selected ? "border-primary text-primary" : "border-neutral text-base-content/60"
              }`}
            >
              <span className="font-medium">{tag.name}</span>
              <select
                aria-label={`Filter by ${tag.name}`}
                value={selected}
                onChange={(e) => setTag(tag, e.target.value)}
                className="max-w-32 cursor-pointer truncate bg-transparent pr-1 text-xs outline-none"
              >
                <option value="">any</option>
                {tag.values.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
                {selected && !tag.values.includes(selected) && (
                  <option value={selected}>{selected}</option>
                )}
              </select>
              {selected && (
                <button
                  type="button"
                  aria-label={`Clear ${tag.name} filter`}
                  onClick={() => setTag(tag, "")}
                  className="rounded-full p-0.5 hover:bg-base-300"
                >
                  <X className="h-3 w-3" />
                </button>
              )}
            </span>
          );
        })}
      </div>
    </div>
  );
}
