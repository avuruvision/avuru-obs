import { test, expect, type Page } from "@playwright/test";

// The Security tab and the proxy page's data-plane figures, stubbed at the
// API. The contract under test is what the screen SAYS: a verdict per
// workload, a caller named when it sends plaintext, and — above all — that a
// data plane nobody read is stated as unread rather than drawn as encrypted.
const PROXIES = {
  proxies: [
    {
      name: "ztunnel.istio-system",
      namespace: "istio-system",
      role: "ztunnel",
      ratePerSec: 40,
      errorRate: 0,
      p50Ms: 1,
      p95Ms: 2,
      callsIn: 2400,
      callsOut: 2380,
      mtlsShare: 1,
      plaintextUnits: 0,
      activeWorkloads: 14,
      pendingWorkloads: 2,
      xdsTerminations: 0,
    },
    {
      name: "istio-proxy.shop",
      namespace: "shop",
      role: "sidecar",
      ratePerSec: 4,
      errorRate: 0,
      p50Ms: 1,
      p95Ms: 2,
      callsIn: 240,
      callsOut: 240,
      mtlsShare: 0.97,
      plaintextUnits: 7,
    },
  ],
};

const SECURITY = {
  available: true,
  state: "ok",
  lastSeen: new Date().toISOString(),
  targets: { up: 12, total: 12 },
  declared: true,
  workloads: [
    {
      namespace: "shop",
      name: "payments",
      service: "payments",
      observed: { reporter: "ztunnel", mtls: 900, plaintext: 0, unknown: 0, requests: 900, connections: 0, mtlsShare: 1 },
      declaredMode: "STRICT",
      declaredScope: "namespace",
      posture: "strict-and-mtls",
    },
    {
      namespace: "shop",
      name: "checkout",
      service: "checkout",
      observed: { reporter: "ztunnel", mtls: 570, plaintext: 30, unknown: 0, requests: 600, connections: 0, mtlsShare: 0.95 },
      declaredMode: "PERMISSIVE",
      declaredScope: "mesh",
      posture: "permissive-with-plaintext-callers",
      plaintextCallers: [{ namespace: "legacy", name: "batch-loader", units: 30 }],
      findings: [
        {
          code: "MESH_PLAINTEXT_CALLERS",
          severity: "warning",
          message: "shop/checkout accepts plaintext from 1 caller",
          hint: "migrate legacy/batch-loader before tightening",
        },
      ],
    },
    {
      // The one this tab exists for: the policy says STRICT and the proxy
      // still saw plaintext, so the policy is not applied.
      namespace: "shop",
      name: "inventory",
      observed: { reporter: "ztunnel", mtls: 0, plaintext: 40, unknown: 0, requests: 40, connections: 0, mtlsShare: 0 },
      declaredMode: "STRICT",
      declaredScope: "namespace",
      posture: "declared-strict-observed-plaintext",
      findings: [
        {
          code: "MESH_MTLS_NOT_ENFORCED",
          severity: "error",
          message: "shop/inventory is declared STRICT and received plaintext",
          hint: "the pod is not enrolled, the selector misses it, or a DestinationRule disables TLS",
        },
      ],
    },
    {
      namespace: "shop",
      name: "reports",
      declaredMode: "STRICT",
      declaredScope: "namespace",
      posture: "uncarried",
    },
  ],
  edges: [],
  findings: [],
};

async function stubScreen(page: Page, security: unknown = SECURITY) {
  await page.route("**/api/v1/capabilities*", (r) =>
    r.fulfill({ json: { version: "test", modules: ["mesh", "infra-metrics"] } }),
  );
  await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
  await page.route("**/api/v1/mesh/control-plane*", (r) =>
    r.fulfill({ json: { available: false, state: "unconfigured" } }),
  );
  await page.route("**/api/v1/mesh/security*", (r) => r.fulfill({ json: security }));
  await page.route("**/api/v1/service-map*", (r) => r.fulfill({ json: { services: [], edges: [] } }));
  await page.route("**/api/v1/metrics/red*", (r) => r.fulfill({ json: { series: [] } }));
}

test.describe("mesh security", () => {
  test("gives every workload a posture, and names the plaintext caller", async ({ page }) => {
    await stubScreen(page);
    await page.goto("/mesh?view=security");

    const tab = page.getByTestId("mesh-security");
    await expect(tab).toContainText("Strict, all mTLS");
    await expect(tab).toContainText("Plaintext callers");
    await expect(tab).toContainText("Not enforced");
    await expect(tab).toContainText("Not carried");
    // Both halves, side by side.
    await expect(tab).toContainText("Declared");
    await expect(tab).toContainText("STRICT");
    // The finding leads with the fix.
    await expect(page.getByTestId("mesh-security-findings")).toContainText("MESH_MTLS_NOT_ENFORCED");

    // The caller is a name, not a count: a count sends someone looking. Scoped
    // to the table — the finding's hint below it names the caller too.
    const table = page.getByTestId("mesh-security-table");
    await expect(table).not.toContainText("legacy/batch-loader");
    await page.getByRole("button", { name: "1 plaintext caller" }).click();
    await expect(table).toContainText("legacy/batch-loader");
  });

  // The column is missing, not full of "default": a policy nobody read must
  // not be rendered as a policy.
  test("drops the Declared column when the cluster was not read, and says why", async ({ page }) => {
    await stubScreen(page, {
      ...SECURITY,
      declared: false,
      workloads: SECURITY.workloads.slice(0, 2).map((w) => ({
        ...w,
        declaredMode: undefined,
        declaredScope: undefined,
        posture: "observed-only-mtls",
      })),
    });
    await page.goto("/mesh?view=security");

    const table = page.getByTestId("mesh-security-table");
    await expect(table).toContainText("Observed mTLS");
    await expect(table).not.toContainText("Declared");
    await expect(page.getByTestId("mesh-security")).toContainText(
      "Declared mode not read — enable the mesh-config module",
    );
  });

  // A data plane nobody scrapes reports zero plaintext, which would read as a
  // fully encrypted mesh. The screen must say "not read" and name the switch,
  // and must show no percentage at all.
  test("states that the data plane is unread instead of reporting 100%", async ({ page }) => {
    await stubScreen(page, {
      available: false,
      state: "unconfigured",
      reason:
        "no data-plane metrics in this window — the sensor scrapes the proxies when mesh.dataPlane.enabled is on",
      declared: false,
      workloads: [],
      edges: [],
      findings: [],
    });
    await page.goto("/mesh?view=security");

    const tab = page.getByTestId("mesh-security");
    await expect(tab).toContainText("Data plane not observed");
    await expect(tab).toContainText("mesh.dataPlane.enabled");
    await expect(tab).not.toContainText("%");
  });

  // "3 of 12" sends someone looking; the pod names tell them where.
  test("names the proxies that did not answer", async ({ page }) => {
    await stubScreen(page, {
      available: false,
      state: "unreachable",
      reason: "the data-plane scrape is running and 2 of its 12 proxy targets are not answering",
      targets: { up: 10, total: 12, down: ["ztunnel-x7k2p", "istio-proxy-checkout-9f1"] },
      declared: false,
      workloads: [],
      edges: [],
      findings: [],
    });
    await page.goto("/mesh?view=security");

    const tab = page.getByTestId("mesh-security");
    await expect(tab).toContainText("Data plane not answering");
    await expect(tab).toContainText("ztunnel-x7k2p");
    await expect(tab).toContainText("istio-proxy-checkout-9f1");
  });

  // A circuit breaker opening looks, from the caller's trace, like any other
  // refused request. The proxy's own flag is the only place it is named.
  test("shows a proxy's requests by response flag, in the proxy's own words", async ({ page }) => {
    await stubScreen(page);
    await page.route("**/api/v1/mesh/workloads/*/*/requests*", (r) =>
      r.fulfill({
        json: {
          measured: true,
          reporter: "destination",
          responseFlags: [
            { flag: "-", requests: 230, meaning: "none" },
            { flag: "UO", requests: 10, meaning: "upstream overflow — circuit breaker open" },
          ],
          destinationVersions: [
            { version: "v2", requests: 200 },
            { version: "v1", requests: 40 },
          ],
          callers: [],
          upstreamStatsHint: "per-upstream counters (pending overflow, outlier ejections) are not collected",
        },
      }),
    );
    await page.goto("/mesh?proxy=istio-proxy.shop");

    const flags = page.getByTestId("mesh-response-flags");
    await expect(flags).toContainText("UO");
    await expect(flags).toContainText("circuit breaker open");
    await expect(page.getByTestId("mesh-destination-versions")).toContainText("v2");
    // The counters a reader looks for next are stated as not collected.
    await expect(page.getByTestId("mesh-requests")).toContainText("not collected");
  });

  // No table of zeros for a workload nobody measured.
  test("says why a workload has no request breakdown", async ({ page }) => {
    await stubScreen(page);
    await page.route("**/api/v1/mesh/workloads/*/*/requests*", (r) =>
      r.fulfill({
        json: {
          measured: false,
          reason: "no data-plane series for shop/istio-proxy in this window",
          responseFlags: [],
          destinationVersions: [],
          callers: [],
          upstreamStatsHint: "…",
        },
      }),
    );
    await page.goto("/mesh?proxy=istio-proxy.shop");

    const section = page.getByTestId("mesh-requests");
    await expect(section).toContainText("no data-plane series for shop/istio-proxy");
    await expect(page.getByTestId("mesh-response-flags")).toHaveCount(0);
  });

  // ztunnel's own account of what it carries. A workload it has been told
  // about and not yet wired is one whose traffic is crossing the node unmeshed,
  // so the waiting count is visible text, not a hover.
  test("shows what a ztunnel carries, and only on a ztunnel", async ({ page }) => {
    await stubScreen(page);
    await page.route("**/api/v1/mesh/workloads/*/*/requests*", (r) =>
      r.fulfill({
        json: { measured: false, responseFlags: [], destinationVersions: [], callers: [], upstreamStatsHint: "…" },
      }),
    );
    await page.goto("/mesh?proxy=ztunnel.istio-system");

    const figures = page.getByTestId("mesh-proxy-figures");
    await expect(figures).toContainText("Workloads carried");
    await expect(figures).toContainText("14");
    await expect(figures).toContainText("2 waiting");
    await expect(figures).toContainText("mTLS");

    // A sidecar carries no fleet: the figure is absent, not zero.
    await page.goto("/mesh?proxy=istio-proxy.shop");
    await expect(figures).toContainText("mTLS");
    await expect(figures).toContainText("97%");
    await expect(figures).not.toContainText("Workloads carried");
  });

  // The proxy table gets the column only when something reported a share.
  test("adds an mTLS column to the proxy table only where the scrape reported one", async ({ page }) => {
    await stubScreen(page);
    await page.goto("/mesh");

    const table = page.getByTestId("mesh-proxies");
    await expect(table).toContainText("mTLS");
    await expect(table).toContainText("97%");

    await page.route("**/api/v1/mesh/proxies*", (r) =>
      r.fulfill({
        json: {
          proxies: PROXIES.proxies.map(({ mtlsShare: _s, plaintextUnits: _p, ...rest }) => rest),
        },
      }),
    );
    await page.reload();
    await expect(table).toContainText("Calls in");
    await expect(table).not.toContainText("mTLS");
  });
});
