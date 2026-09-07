import { test, expect, type Page } from "@playwright/test";

// The Workloads tab and a workload's page, stubbed at the API. The contract
// under test is what the screen SAYS about a cluster: which workloads exist
// whether or not they sent a span, what was declared for each and where that
// came from, and what a waypoint serves.
const CAPABILITIES = {
  version: "test",
  modules: ["core", "logs", "infra-metrics", "mesh", "mesh-config"],
};

const PROXIES = {
  proxies: [
    {
      name: "global-waypoint.istio-waypoint",
      namespace: "istio-waypoint",
      role: "waypoint",
      ratePerSec: 4,
      errorRate: 0,
      p50Ms: 1,
      p95Ms: 2,
      callsIn: 240,
      callsOut: 238,
    },
    {
      name: "checkout",
      namespace: "shop",
      role: "sidecar",
      ratePerSec: 2,
      errorRate: 0,
      p50Ms: 1,
      p95Ms: 2,
      callsIn: 100,
      callsOut: 100,
    },
  ],
};

const STRICT_BY_NAMESPACE = { mode: "STRICT", source: "namespace", policy: "default" };
const NAMESPACE_POLICY = {
  kind: "PeerAuthentication",
  namespace: "shop",
  name: "default",
  scope: "namespace",
};

const WEB = {
  namespace: "shop",
  name: "web",
  kind: "Deployment",
  declaredMode: "ambient",
  dataplaneMode: "ambient",
  injected: false,
  captured: true,
  waypoint: "global-waypoint",
  waypointNamespace: "istio-waypoint",
  waypointSource: "namespace",
  serviceAccount: "web",
  pods: 2,
  runningPods: 2,
  declaredMtls: STRICT_BY_NAMESPACE,
  hasTraffic: true,
  ratePerSec: 12,
  errorRate: 0,
  services: ["web"],
  policies: [NAMESPACE_POLICY],
  errors: 0,
  warnings: 0,
};

// The enrolment gap: asked for, running, and neither injected nor captured. It
// sends nothing of its own, so no traffic-derived screen has a row for it.
const REPORTS = {
  namespace: "shop",
  name: "reports",
  kind: "Deployment",
  declaredMode: "ambient",
  injected: false,
  captured: false,
  serviceAccount: "reports",
  pods: 1,
  runningPods: 1,
  declaredMtls: STRICT_BY_NAMESPACE,
  hasTraffic: false,
  services: [],
  policies: [NAMESPACE_POLICY],
  errors: 1,
  warnings: 0,
};

const BATCH = {
  namespace: "jobs",
  name: "batch-loader",
  kind: "Job",
  injected: false,
  captured: false,
  pods: 1,
  runningPods: 0,
  hasTraffic: false,
  services: [],
  policies: [],
  errors: 0,
  warnings: 0,
};

const WORKLOADS = [WEB, REPORTS, BATCH];

const NOT_ENROLLED = {
  code: "MESH_WORKLOAD_NOT_ENROLLED",
  severity: "error",
  message: "the namespace asks for ambient and no pod of this workload is captured",
  hint: "check that the node agent runs on the pod's node and the pod is not excluded",
};

const POLICY_FINDING = {
  code: "MESH_PEERAUTH_PERMISSIVE_PORT",
  severity: "warning",
  message: "port 9090 is PERMISSIVE under a STRICT policy",
  hint: "drop the port override, or say so in the policy's name",
};

const ROUTE_FINDING = {
  code: "MESH_ROUTE_PARENT_MISSING",
  severity: "error",
  message: "parentRef shop/edge names a Gateway that does not exist",
  hint: "create the Gateway, or correct the parentRef — until then nothing serves this route",
};

// The page: the row, the cluster's record of it, its pods with their rollout,
// and every piece of configuration that names it — a policy by label, a route
// through its Service — each with its own findings.
const REPORTS_DETAIL = {
  state: "ok",
  syncedAt: new Date().toISOString(),
  workload: {
    ...REPORTS,
    createdAt: "2026-07-09T10:23:00Z",
    createdFrom: "controller",
    app: "reports",
    version: "v2",
    waypoint: "global-waypoint",
    waypointNamespace: "istio-waypoint",
    waypointSource: "namespace",
    services: ["shop/reports"],
    policies: [{ ...NAMESPACE_POLICY, findings: [POLICY_FINDING] }],
  },
  labels: { app: "reports", version: "v2", "avuru.io/tier": "T1" },
  annotations: { "deployment.kubernetes.io/revision": "3" },
  health: { status: "down", reason: "none of 1 pods is running" },
  routes: [
    {
      kind: "HTTPRoute",
      namespace: "shop",
      name: "reports-route",
      service: "shop/reports",
      host: "reports",
      findings: [ROUTE_FINDING],
    },
  ],
  findings: [NOT_ENROLLED],
  pods: [
    {
      name: "reports-7c9d-x1",
      node: "node-a",
      phase: "Running",
      revision: "7c9d",
      createdAt: "2026-07-09T11:23:00Z",
      injected: false,
      captured: false,
    },
  ],
  podsShown: 1,
  podsTotal: 1,
};

const PODS_CUT =
  "the snapshot keeps the first 500 of the cluster's 900 pods, so workloads past the cut are not listed and the checks that need every pod did not run — an empty issues column here is not a clean bill";

const NAMESPACES = {
  state: "ok",
  syncedAt: new Date().toISOString(),
  podsTruncated: true,
  checksSkipped: PODS_CUT,
  namespaces: [
    {
      name: "shop",
      dataplaneMode: "ambient",
      waypoint: "global-waypoint",
      waypointNamespace: "istio-waypoint",
      mtlsMode: "STRICT",
      mtlsSource: "mesh",
      mtlsPolicy: "default",
      services: 4,
      workloads: 2,
      enrolled: 1,
      errors: 1,
      warnings: 0,
    },
    // No policy reaches it: the mesh default governs, which was not read.
    { name: "outside", services: 2, errors: 0, warnings: 0 },
  ],
};

const SERVES = {
  state: "ok",
  waypoint: { namespace: "istio-waypoint", name: "global-waypoint", scope: "service", running: true },
  namespaces: ["shop"],
  services: ["shop/web"],
  workloads: ["shop/reports"],
};

// Every endpoint the mesh screen reaches for, stubbed, so a test only has to
// say what differs.
async function stubMesh(page: Page, workloads: object = { state: "ok", workloads: WORKLOADS }) {
  await page.route("**/api/v1/capabilities*", (r) => r.fulfill({ json: CAPABILITIES }));
  await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
  await page.route("**/api/v1/mesh/control-plane*", (r) =>
    r.fulfill({ json: { available: false, state: "unconfigured" } }),
  );
  await page.route("**/api/v1/mesh/namespaces*", (r) => r.fulfill({ json: NAMESPACES }));
  await page.route("**/api/v1/mesh/workloads?*", (r) => {
    // The hub owns the mode filter; the stub applies the same rule.
    const mode = new URL(r.request().url()).searchParams.get("mode");
    const body = workloads as {
      workloads?: { declaredMode?: string; injected: boolean; captured: boolean }[];
    };
    const rows = (body.workloads ?? []).filter((w) =>
      mode === "declared-only" ? !!w.declaredMode && !w.injected && !w.captured : true,
    );
    return r.fulfill({ json: { ...workloads, workloads: rows } });
  });
  await page.route("**/api/v1/mesh/workloads/shop/reports*", (r) =>
    r.fulfill({ json: REPORTS_DETAIL }),
  );
  await page.route("**/api/v1/mesh/waypoints/istio-waypoint/global-waypoint*", (r) =>
    r.fulfill({ json: SERVES }),
  );
  await page.route("**/api/v1/service-map*", (r) => r.fulfill({ json: { services: [], edges: [] } }));
  await page.route("**/api/v1/metrics/red*", (r) => r.fulfill({ json: { series: [] } }));
}

test.describe("mesh workloads", () => {
  test("lists every workload the cluster runs, enrolled or not", async ({ page }) => {
    await stubMesh(page);
    await page.goto("/mesh");

    await page.getByRole("tab", { name: "Workloads" }).click();
    await expect(page).toHaveURL(/view=workloads/);

    const table = page.getByTestId("mesh-workloads");
    await expect(table).toContainText("web");
    await expect(table).toContainText("batch-loader");
    // The row that exists because the list comes from pods: no traffic, and
    // the gap named as such.
    const reports = table.locator("tbody tr", { hasText: "reports" });
    await expect(reports).toContainText("declared, not enrolled");
    await expect(reports).toContainText("—");
    await expect(table).toContainText("captured");
    await expect(table).toContainText("out of mesh");
  });

  test("filters to the enrolment gap, and keeps it in the URL", async ({ page }) => {
    await stubMesh(page);
    await page.goto("/mesh?view=workloads");

    await page.getByRole("button", { name: "Filter by mode" }).click();
    await page.getByRole("option", { name: "Declared, not enrolled" }).click();
    await expect(page).toHaveURL(/mode=declared-only/);

    const table = page.getByTestId("mesh-workloads");
    await expect(table).toContainText("reports");
    await expect(table).not.toContainText("batch-loader");
    await expect(table).not.toContainText("shop/web");

    // A pasted link lands on the same filtered list.
    await page.goto("/mesh?view=workloads&mode=declared-only");
    await expect(table).toContainText("reports");
    await expect(table).not.toContainText("batch-loader");
  });

  test("opens a workload: declared beside observed, and what decided it", async ({ page }) => {
    await stubMesh(page);
    await page.goto("/mesh?view=workloads");

    await page.getByRole("button", { name: "shop/reports" }).click();
    await expect(page).toHaveURL(/wl=shop(%2F|\/)reports/);

    const mtls = page.getByTestId("mesh-workload-mtls");
    await expect(mtls).toContainText("STRICT");
    await expect(mtls).toContainText("default");
    // Nothing measured it; the page says so instead of showing 0%.
    await expect(mtls).toContainText("—");
    await expect(mtls).toContainText("Nothing measured");

    const policies = page.getByTestId("mesh-workload-policies");
    await expect(policies).toContainText("PeerAuthentication");
    await expect(policies).toContainText("shop/default");
    await expect(policies).toContainText("MESH_PEERAUTH_PERMISSIVE_PORT");
    await expect(policies.getByRole("link", { name: "shop/default" })).toHaveAttribute(
      "href",
      /view=config.*object=PeerAuthentication/,
    );

    const findings = page.getByTestId("mesh-workload-findings");
    await expect(findings).toContainText("no pod of this workload is captured");
    // The fix, not just the fault.
    await expect(findings).toContainText("check that the node agent runs");
    await expect(findings).toContainText("MESH_WORKLOAD_NOT_ENROLLED");

    await expect(page.getByRole("link", { name: /Traces, logs/ })).toHaveAttribute(
      "href",
      "/services?service=reports",
    );

    await page.getByRole("button", { name: "All workloads" }).click();
    await expect(page.getByTestId("mesh-workloads")).toBeVisible();
  });

  test("a workload's page reads like the cluster's record of it", async ({ page }) => {
    await stubMesh(page);
    await page.goto("/mesh?view=workloads&wl=shop%2Freports");

    // The verdict, with its reason a hover away.
    const health = page.getByTestId("mesh-workload-health");
    await expect(health).toContainText("Down");
    await expect(health).toHaveAttribute("title", /none of 1 pods/);

    const overview = page.getByTestId("mesh-workload-overview");
    await expect(overview).toContainText("Deployment");
    await expect(overview).toContainText("v2");
    await expect(overview).toContainText("ago");
    // Labels as chips, without the ReplicaSet's hash; the controller's
    // annotations behind a fold.
    await expect(page.getByTestId("mesh-workload-labels")).toContainText("avuru.io/tier=T1");
    await expect(overview).not.toContainText("pod-template-hash");
    await expect(overview).toContainText("Annotations (1)");

    // Related: the Service by its own screen, the waypoint by its proxy page.
    await expect(overview.getByRole("link", { name: "shop/reports", exact: true })).toHaveAttribute(
      "href",
      "/services?service=reports",
    );
    await expect(overview.getByRole("link", { name: /global-waypoint/ })).toHaveAttribute(
      "href",
      "/mesh?proxy=global-waypoint.istio-waypoint",
    );

    // Each pod with the rollout it belongs to.
    const pods = page.getByTestId("mesh-workload-pods");
    await expect(pods).toContainText("reports-7c9d-x1");
    await expect(pods).toContainText("7c9d");
    await expect(pods).toContainText("node-a");
    await expect(pods).toContainText("not enrolled");

    // Policies and routes in one section, each linking into the config
    // browser and carrying its own finding.
    const config = page.getByTestId("mesh-workload-istio-config");
    await expect(config).toContainText("PeerAuthentication");
    await expect(config).toContainText("HTTPRoute");
    await expect(config).toContainText("via shop/reports");
    await expect(config).toContainText("MESH_ROUTE_PARENT_MISSING");
    await expect(config.getByRole("link", { name: "shop/reports-route" })).toHaveAttribute(
      "href",
      /view=config.*object=HTTPRoute/,
    );
  });

  test("namespaces say where their mode came from, and how many are enrolled", async ({ page }) => {
    await stubMesh(page);
    await page.goto("/mesh?view=namespaces");

    const table = page.getByTestId("mesh-namespaces");
    await expect(table).toContainText("Enrolled");
    const shop = table.locator("tbody tr", { hasText: "shop" });
    await expect(shop).toContainText("STRICT");
    // A mesh-wide policy decided it: editing this namespace changes nothing.
    await expect(shop).toContainText("inherited");
    await expect(shop).toContainText("1/2");
    // No policy at all is a stated answer, not a blank.
    await expect(table.locator("tbody tr", { hasText: "outside" })).toContainText("default");

    // The pod list was cut, and the tab says what that means for the columns.
    await expect(page.getByText(/first 500 of the cluster's 900 pods/)).toBeVisible();
    await expect(page.getByText(/not a clean bill/)).toBeVisible();
  });

  test("a waypoint says what it serves; a sidecar has nothing to say", async ({ page }) => {
    await stubMesh(page);
    await page.goto("/mesh?proxy=global-waypoint.istio-waypoint");

    const serves = page.getByTestId("mesh-waypoint-serves");
    await expect(serves).toContainText("Serves");
    await expect(serves).toContainText("shop/web");
    await expect(serves).toContainText("shop/reports");
    await expect(serves.getByRole("link", { name: "shop", exact: true })).toHaveAttribute(
      "href",
      "/mesh?view=workloads&wlns=shop",
    );
    await expect(serves).not.toContainText("Nothing runs this waypoint");

    await page.goto("/mesh?proxy=checkout");
    await expect(page.getByRole("heading", { name: "checkout" })).toBeVisible();
    await expect(page.getByTestId("mesh-waypoint-serves")).toHaveCount(0);
  });

  // A cluster we may not read must say so, and name the fix.
  test("names the ClusterRole when the cluster cannot be read", async ({ page }) => {
    await stubMesh(page, {
      state: "forbidden",
      reason: "the hub may not read mesh configuration — grant the avuruobs-mesh-config ClusterRole",
      workloads: [],
    });
    await page.goto("/mesh?view=workloads");

    await expect(page.getByText("Not allowed to read the cluster")).toBeVisible();
    await expect(page.getByText(/avuruobs-mesh-config/)).toBeVisible();
    // An empty table would have reported a cluster running nothing.
    await expect(page.getByTestId("mesh-workloads")).toHaveCount(0);
  });
});
