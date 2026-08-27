import type { SelectOption } from "@/components/ui/select";

// The namespace business tags are mapped into at collection. Mirrors
// clickhouse.TagPrefix: it is the one resource-attribute family the trace
// filter resolves against the workload rather than the span.
const TAG_PREFIX = "avuru.tag.";

// The grouping vocabulary offered in the UI. It is a subset of what the API
// accepts (any attribute or resource key works there) - these are the keys that
// answer a question people actually ask, and an open key picker can come when
// someone needs one.
export const GROUP_BY_OPTIONS: SelectOption[] = [
  { value: "service", label: "Service" },
  { value: "operation", label: "Operation" },
  { value: "status", label: "Outcome" },
  { value: "kind", label: "Span kind" },
  { value: "attribute:http.route", label: "HTTP route" },
  { value: "attribute:http.request.method", label: "HTTP method" },
  { value: "attribute:db.system", label: "Database" },
  { value: "attribute:messaging.system", label: "Messaging system" },
  { value: "resource:k8s.namespace.name", label: "Namespace" },
  { value: "resource:deployment.environment", label: "Environment" },
];

export const SCOPE_OPTIONS: SelectOption[] = [
  { value: "entry", label: "Requests served" },
  { value: "root", label: "Trace entry points" },
  { value: "all", label: "All spans" },
];

// What each scope actually counts, said plainly - the difference between them
// is the difference between three genuinely different questions, and a reader
// who picks the wrong one gets a correct chart of something they did not ask.
export const SCOPE_HELP: Record<string, string> = {
  entry: "Server and consumer spans - what each service was asked to do.",
  root: "Parentless spans only - one per trace, where traffic entered the estate.",
  all: "Every span, including outgoing calls. Total time double-counts nesting.",
};

/**
 * The trace-list filter a breakdown slice drills into, or null when this
 * dimension has no faithful equivalent.
 *
 * Returning null is a feature. A "span kind" slice has no filter behind it, and
 * a resource key is matched against span attributes by the tag filter - so
 * offering either as a click would hand back a different set of traces than the
 * slice the user pointed at. Nothing happens instead.
 */
export function drillFilter(
  groupBy: string,
  key: string,
): Record<string, string> | null {
  switch (groupBy) {
    case "service":
      return { service: key, tab: "traces" };
    case "operation":
      return { operation: key, tab: "traces" };
    case "status":
      return { status: key, tab: "traces" };
    default:
      break;
  }
  // Span attributes share the trace filter's tag vocabulary exactly.
  if (groupBy.startsWith("attribute:")) {
    return { tags: `${groupBy.slice("attribute:".length)}=${key}`, tab: "traces" };
  }
  // Resource keys only when they are business tags: those are the ones the tag
  // filter resolves against the workload rather than the span.
  if (groupBy.startsWith("resource:")) {
    const attr = groupBy.slice("resource:".length);
    if (attr.startsWith(TAG_PREFIX)) return { tags: `${attr}=${key}`, tab: "traces" };
  }
  return null;
}
