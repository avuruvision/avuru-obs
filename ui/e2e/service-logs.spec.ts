import { test, expect } from "@playwright/test";

// The service Logs tab, stubbed at the API. The thing under test is the shape
// this feature exists for: a service whose lines are NOT filed under its own
// name — valife-report-service runs as the workload valife-report — so the
// interesting states are what the toolbar offers and what the tab says when
// the two cannot be tied together. Neither is reproducible against the seed,
// which has no service mesh in it.
// A seeded service, so the page around the tab (header, RED tiles, tabs)
// renders for real and only the endpoint under test is faked.
const SERVICE = "seed-payments";
// The workload the hub resolved it to — the whole point of the feature is that
// this is NOT the service name.
const WORKLOAD = "payments";

const LINE = (body: string, severity = "ERROR") => ({
  timestamp: new Date().toISOString(),
  severity,
  service: WORKLOAD,
  body,
  traceId: "",
  spanId: "",
});

// What the hub answers when it tied the service to a workload: the app source
// carries BOTH names, and the proxies are on offer.
const RESOLVED = {
  logs: [
    LINE("Unexpected error: relation \"repeat_flow\" does not exist"),
    LINE("Cannot invoke \"java.math.BigDecimal.abs()\" because \"act\" is null"),
    LINE("started in 4.2s", "INFO"),
  ],
  nextCursor: "",
  sources: {
    app: [WORKLOAD, `${WORKLOAD}.shop`, SERVICE],
    ztunnel: ["ztunnel"],
    waypoint: ["global-waypoint", "global-waypoint.istio-waypoint"],
    needles: ["payments.shop.svc", "payments-7c9d-x1"],
    precise: true,
    workload: WORKLOAD,
    namespace: "shop",
  },
};

// What it answers when it could not: the app's own name alone, no proxy
// sources, and a sentence naming what to change.
const UNRESOLVED = {
  logs: [LINE("started in 4.2s", "INFO")],
  nextCursor: "",
  sources: {
    app: [SERVICE],
    ztunnel: [],
    waypoint: [],
    needles: [],
    precise: false,
    proxiesUnavailable:
      "the proxies' lines need a workload: this service's spans carry no k8s.deployment.name, which the collector's k8sattributes processor is what adds, and mesh-config is off",
  },
};

async function stub(page: import("@playwright/test").Page, body: unknown) {
  await page.route(`**/api/v1/services/${SERVICE}/logs*`, (r) => r.fulfill({ json: body }));
}

test.describe("service logs tab", () => {
  test("shows the workload's lines under the service's name, proxies one click away", async ({
    page,
  }) => {
    const asked: string[] = [];
    await stub(page, RESOLVED);
    page.on("request", (r) => {
      if (r.url().includes(`/services/${SERVICE}/logs`)) asked.push(r.url());
    });

    await page.goto(`/services?service=${SERVICE}&view=logs`);

    // The lines the mesh page shows, on the service page.
    const table = page.getByTestId("sourced-logs");
    await expect(table).toContainText("repeat_flow");

    // The app source is the union of both names — this is the actual fix for
    // the empty tab, so it is asserted where a reader can see it.
    const sources = page.getByTestId("sourced-log-sources");
    await expect(sources).toContainText(WORKLOAD);
    await expect(sources).toContainText(SERVICE);
    await expect(sources).toContainText("1 pod matched");

    // The default is the app's own lines, and the default stays out of the URL.
    expect(asked.at(-1)).toContain("source=app");
    expect(asked.at(-1)).not.toContain("source=app%2Cztunnel");
    expect(page.url()).not.toContain("src=");

    // Ticking a proxy widens the same stream rather than opening another.
    await page.getByRole("checkbox", { name: "ztunnel" }).check();
    await expect.poll(() => page.url()).toContain("src=app%2Cztunnel");
    await expect.poll(() => asked.at(-1)).toContain("source=app%2Cztunnel");

    // A resolved workload is reachable from here.
    await expect(
      sources.getByRole("link", { name: "open the workload" }),
    ).toHaveAttribute("href", /wl=shop%2Fpayments&wltab=logs/);
  });

  test("says why the proxies are not on offer instead of hiding the gap", async ({ page }) => {
    await stub(page, UNRESOLVED);
    await page.goto(`/services?service=${SERVICE}&view=logs`);

    // No workload behind the name: the checkboxes would be a filter that
    // cannot answer, so they are not shown — and the reason is.
    await expect(page.getByTestId("sourced-log-sources")).toContainText(
      "k8s.deployment.name",
    );
    await expect(page.getByRole("checkbox", { name: "ztunnel" })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "open the workload" })).toHaveCount(0);
    // The tab still does its old job.
    await expect(page.getByTestId("sourced-logs")).toContainText("started in 4.2s");
  });

  test("the search and severity filters reach the hub", async ({ page }) => {
    const asked: string[] = [];
    await stub(page, RESOLVED);
    page.on("request", (r) => {
      if (r.url().includes(`/services/${SERVICE}/logs`)) asked.push(r.url());
    });
    await page.goto(`/services?service=${SERVICE}&view=logs`);

    await page.getByRole("searchbox", { name: "Search logs" }).fill("repeat_flow");
    await page.getByRole("searchbox", { name: "Search logs" }).press("Enter");
    await expect.poll(() => asked.at(-1)).toContain("q=repeat_flow");

    await page.getByRole("button", { name: "Minimum severity" }).click();
    await page.getByRole("option", { name: "ERROR+" }).click();
    await expect.poll(() => asked.at(-1)).toContain("severity=ERROR");
  });

  test("the loaded lines can be copied whole or by selection", async ({ page }) => {
    await stub(page, RESOLVED);
    await page.goto(`/services?service=${SERVICE}&view=logs`);

    // The control says its count, because "Copy" over a list that grows as you
    // scroll would be a lie about what you get.
    const actions = page.getByTestId("log-table-actions");
    await expect(actions.getByRole("button", { name: "Copy 3 lines" })).toBeVisible();
    await expect(actions.getByRole("button", { name: /Download \.log/ })).toBeVisible();

    await page.getByRole("checkbox", { name: "Select log line 1" }).click();
    await page.getByRole("checkbox", { name: "Select log line 2" }).click();
    await expect(actions.getByRole("button", { name: "Copy 2 selected" })).toBeVisible();

    await actions.getByRole("button", { name: "Clear selection" }).click();
    await expect(actions.getByRole("button", { name: "Copy 3 lines" })).toBeVisible();
  });
});
