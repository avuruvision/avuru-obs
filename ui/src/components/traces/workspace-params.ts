// The URL params owned by the trace workspace. Every exit path (close button,
// tab switch, service click-throughs) must clear the same set — import this
// instead of restating the list so the sites can't drift.
export const CLEAR_WORKSPACE_PARAMS = {
  trace: undefined,
  view: undefined,
  span: undefined,
  compare: undefined,
  full: undefined,
  focus: undefined,
} as const satisfies Record<string, undefined>;
