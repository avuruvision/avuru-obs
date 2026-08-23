"use client";

import { usePathname } from "next/navigation";
import { ExternalLink } from "lucide-react";

import { docsUrl } from "./nav-config";

// A link to the page of the manual that explains the screen you are on.
//
// Cheap, and it changes what the product feels like: every screen answers
// "what am I looking at?" without a search. Rendered from the same nav model
// the sidebar and breadcrumbs use, so a new screen gets its link by declaring
// where its documentation lives — and a screen with no page yet renders
// nothing, because a link that 404s is worse than no link.
export function DocsLink() {
  const url = docsUrl(usePathname());
  if (!url) return null;

  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer noopener"
      data-testid="docs-link"
      title="Open the documentation for this screen"
      className="flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-xs text-base-content/45 hover:bg-base-200 hover:text-base-content"
    >
      docs
      <ExternalLink className="h-3 w-3" aria-hidden />
      <span className="sr-only">(opens in a new tab)</span>
    </a>
  );
}
