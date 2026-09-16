import { test, expect } from "@playwright/test";
import { buildModel, podKey } from "../src/components/infra/xray/model";
import type { PodConnection, PodStats } from "../src/lib/api-types";

const pod = (namespace: string, node: string): PodStats => ({ name: "api", namespace, node, cpuUsageCores: .2, memoryUsageBytes: 1024 });
const a = pod("shop", "node-a"), b = pod("billing", "node-b");
const edge: PodConnection = { source: { ...a, service: "caller" }, target: { ...b, service: "callee" }, calls: 12, errors: 0, p95Ms: 8 };

test("Pod namesakes keep distinct identities and connect to the recorded replica", () => {
  const model = buildModel([], [a, b], [edge]);
  expect(model.pods).toHaveLength(2);
  expect(model.flows[0]).toMatchObject({ source: podKey(a), target: podKey(b) });
});

test("a filtered or missing known destination never becomes an unresolved peer", () => {
  const model = buildModel([], [a], [{ ...edge, peer: "a-recorded-address" }]);
  expect(model.flows).toEqual([]);
  const unknown = { ...edge, target: { name: "", namespace: "", node: "", service: "" }, peer: "api.example.test" };
  expect(buildModel([], [a], [unknown]).flows[0].target).toBeUndefined();
});

test("ambiguous names and mismatching recorded nodes do not guess placement", () => {
  const namesake = { ...a, node: "another-cluster-node" };
  expect(buildModel([], [a, namesake, b], [{ ...edge, source: { ...edge.source, node: "" } }]).flows).toEqual([]);
  expect(buildModel([], [a, b], [{ ...edge, target: { ...edge.target, node: "old-node" } }]).flows).toEqual([]);
});

test("metric changes never shuffle placement and omitted Pods cannot create globe flows", () => {
  const many = Array.from({ length: 30 }, (_, i) => ({ ...a, name: `api-${String(i).padStart(2, "0")}` }));
  const one = buildModel([], many, []), two = buildModel([], many.toReversed().map(p => ({ ...p, cpuUsageCores: 9 })), []);
  expect(one.pods.map(p => [p.id, p.position])).toEqual(two.pods.map(p => [p.id, p.position]));
  expect(one.podTotal).toBe(30); expect(one.pods).toHaveLength(12);
  const omitted = { ...edge, source: { ...many[0], service: "source" }, target: { ...many[29], service: "target" } };
  expect(buildModel([], many, [omitted]).flows).toEqual([]);
});
