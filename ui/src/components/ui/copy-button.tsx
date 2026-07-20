"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/cn";

// A small copy-to-clipboard control with the codebase's Copy→Check feedback
// (1500ms; there is no toast system). Icon-only by default; pass `label` for a
// labelled control. Caller controls placement/reveal via `className` (e.g.
// `invisible group-hover:visible` for hover-reveal in a row). Clipboard failures
// (insecure context) are swallowed — copy is best-effort.
export function CopyButton({
  value,
  label,
  ariaLabel,
  className,
  iconClass = "h-3.5 w-3.5",
}: {
  value: string;
  label?: string;
  ariaLabel?: string;
  className?: string;
  iconClass?: string;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard blocked (insecure context) — no-op
    }
  };
  return (
    <button
      type="button"
      aria-label={ariaLabel ?? (label ? `Copy ${label}` : "Copy")}
      onClick={copy}
      className={cn(
        "inline-flex shrink-0 items-center gap-1 text-base-content/40 hover:text-base-content",
        className,
      )}
    >
      {copied ? <Check className={iconClass} /> : <Copy className={iconClass} />}
      {label && <span className="text-xs">{copied ? "Copied" : label}</span>}
    </button>
  );
}
