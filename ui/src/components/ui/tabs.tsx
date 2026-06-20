"use client";

import { cn } from "@/lib/cn";

export interface TabItem<T extends string> {
  value: T;
  label: string;
}

// Coroot-style underline tabs.
export function Tabs<T extends string>({
  items,
  value,
  onChange,
  className,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (value: T) => void;
  className?: string;
}) {
  return (
    <div
      role="tablist"
      className={cn("flex gap-5 border-b border-neutral", className)}
    >
      {items.map((item) => (
        <button
          key={item.value}
          role="tab"
          aria-selected={item.value === value}
          onClick={() => onChange(item.value)}
          className={cn(
            "-mb-px border-b-2 px-0.5 pb-2 text-xs font-semibold uppercase tracking-wider transition-colors",
            item.value === value
              ? "border-primary text-primary"
              : "border-transparent text-base-content/50 hover:text-base-content",
          )}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
