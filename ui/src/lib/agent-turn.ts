// One agent turn, as the graph it actually is.
//
// A turn is not a list. It is a model call that decides, a fan-out to tools,
// results coming back, and often another model call after — and the questions
// an operator has about it are graph questions: which tool is slow, which one
// fails, how many hops before it converges, which tool a retry loop is stuck
// on. The span tree can only answer those by being counted by hand.
//
// This is a sibling of buildTracePath, not a variant of it. That one groups by
// SERVICE, which is the right unit for a request crossing an estate and the
// wrong one here: every span of a turn usually belongs to one service, so the
// service view collapses the whole turn into a single node. The unit here is
// the model or tool being called.
//
// Self-time weighting carries over unchanged, and for the same reason it was
// right there: the model-call span CONTAINS its tool spans, so wall-clock
// duration would report the model as responsible for time the tools spent.

import { childrenByParent, selfTimeMs } from "@/lib/trace";
import { spanStatus } from "@/lib/span-status";
import type { Span } from "@/lib/api-types";

/** The gen_ai operation values that open a turn. Mirrors the hub's agent class. */
const AGENT_OPS = new Set(["invoke_agent", "create_agent"]);
const TOOL_OP = "execute_tool";

export type TurnNodeKind = "agent" | "model" | "tool";

export interface TurnNode {
  /** Stable key: the kind and the name, so a repeated tool folds into one node. */
  id: string;
  kind: TurnNodeKind;
  /** Model id, tool name, or agent name — whatever names the thing being called. */
  label: string;
  /**
   * How many times this turn hit it. A tool a turn called four times is ONE
   * node with a count, because the loop is the thing worth seeing — four
   * identical cards say the same thing and hide that it repeated.
   */
  calls: number;
  /**
   * Time spent INSIDE this node: the sum of its spans' self time. A model span
   * contains the tool spans it triggered, so duration would double-count.
   */
  selfMs: number;
  errorCount: number;
  refusedCount: number;
  /** Order of first appearance within the turn — the sequence the turn ran in. */
  step: number;
  /** The span to select when this node is clicked — its earliest one. */
  firstSpanId: string;
}

export interface TurnEdge {
  source: string;
  target: string;
  calls: number;
  errorCount: number;
}

export interface AgentTurn {
  /** The invoke_agent span this turn was built from. */
  rootSpanId: string;
  agentName: string;
  /** Wall-clock duration of the whole turn. */
  durationMs: number;
  nodes: TurnNode[];
  edges: TurnEdge[];
}

function op(span: Span): string {
  return span.attributes?.["gen_ai.operation.name"] ?? "";
}

/** True when this span opens an agent turn. */
export function isAgentSpan(span: Span): boolean {
  return AGENT_OPS.has(op(span));
}

/**
 * The agent turns in a trace, in start order. Empty for an ordinary request,
 * which is how the trace panel decides whether to offer this view at all.
 */
export function agentTurnRoots(spans: Span[]): Span[] {
  return spans
    .filter(isAgentSpan)
    .slice()
    .sort((a, b) => a.startTime.localeCompare(b.startTime));
}

/**
 * Names the thing a span called. The model that ANSWERED wins over the one
 * requested, exactly as the AI tables resolve it: an alias resolves at the
 * provider, and the response is what a bill is computed against.
 */
function labelOf(span: Span, kind: TurnNodeKind): string {
  const a = span.attributes ?? {};
  if (kind === "tool") return a["gen_ai.tool.name"] || span.operation;
  if (kind === "model") {
    return (
      a["gen_ai.response.model"] || a["gen_ai.request.model"] || span.operation
    );
  }
  return a["gen_ai.agent.name"] || span.operation;
}

function kindOf(span: Span): TurnNodeKind | null {
  const o = op(span);
  if (AGENT_OPS.has(o)) return "agent";
  if (o === TOOL_OP) return "tool";
  // Anything else carrying gen_ai is a call to a model — the same complement
  // rule the hub uses, so an operation this build has not heard of is
  // mis-labelled rather than made invisible.
  const a = span.attributes ?? {};
  if (o || a["gen_ai.system"] || a["gen_ai.provider.name"]) return "model";
  return null;
}

/**
 * Builds the graph of ONE turn, scoped to the subtree of its agent span.
 *
 * Non-gen_ai spans inside the subtree are skipped as nodes but still counted
 * against their parent's self time, because the time they took is real and
 * attributing it to the model would say the model was slow when a database was.
 */
export function buildAgentTurn(
  spans: Span[],
  rootSpanId: string,
): AgentTurn | null {
  const root = spans.find((s) => s.spanId === rootSpanId);
  if (!root || !isAgentSpan(root)) return null;

  const byParent = childrenByParent(spans);

  // The subtree, in start order, so `step` reflects the sequence the turn ran.
  const subtree: Span[] = [];
  const walk = (span: Span) => {
    subtree.push(span);
    for (const child of byParent.get(span.spanId) ?? []) walk(child);
  };
  walk(root);
  subtree.sort((a, b) => a.startTime.localeCompare(b.startTime));

  const nodes = new Map<string, TurnNode>();
  const edges = new Map<string, TurnEdge>();
  // Which node a span contributed to, so a child can find its parent's node
  // even when spans sit between them.
  const nodeOfSpan = new Map<string, string>();

  for (const span of subtree) {
    const kind = kindOf(span);
    if (!kind) continue;

    const label = labelOf(span, kind);
    const id = `${kind}:${label}`;
    const status = spanStatus(span).kind;
    const children = byParent.get(span.spanId) ?? [];
    const self = selfTimeMs(span, children);

    const existing = nodes.get(id);
    if (existing) {
      existing.calls++;
      existing.selfMs += self;
      if (status === "error") existing.errorCount++;
      if (status === "refused") existing.refusedCount++;
    } else {
      nodes.set(id, {
        id,
        kind,
        label,
        calls: 1,
        selfMs: self,
        errorCount: status === "error" ? 1 : 0,
        refusedCount: status === "refused" ? 1 : 0,
        step: nodes.size,
        firstSpanId: span.spanId,
      });
    }
    nodeOfSpan.set(span.spanId, id);

    // Edge to the nearest ANCESTOR that is itself a node. Walking up rather
    // than reading parentSpanId directly keeps an HTTP client span between a
    // model call and its tool from breaking the turn into two graphs.
    let cursorId = span.parentSpanId;
    const seen = new Set<string>([span.spanId]);
    while (cursorId && !seen.has(cursorId)) {
      seen.add(cursorId);
      const parentNode = nodeOfSpan.get(cursorId);
      if (parentNode) {
        if (parentNode !== id) {
          const key = `${parentNode}->${id}`;
          const e = edges.get(key);
          if (e) {
            e.calls++;
            if (status === "error") e.errorCount++;
          } else {
            edges.set(key, {
              source: parentNode,
              target: id,
              calls: 1,
              errorCount: status === "error" ? 1 : 0,
            });
          }
        }
        break;
      }
      cursorId = spans.find((s) => s.spanId === cursorId)?.parentSpanId ?? "";
    }
  }

  return {
    rootSpanId,
    agentName: labelOf(root, "agent"),
    durationMs: root.durationMs,
    nodes: [...nodes.values()],
    edges: [...edges.values()],
  };
}
