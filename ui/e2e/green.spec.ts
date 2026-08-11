import { readFileSync } from "node:fs";
import { test, expect } from "@playwright/test";

// Seeded green fixtures (deploy/compose/seed/fixtures/metrics_kepler.json +
// metrics_kubeletstats_pods.json) with the mounted factor config
// (deploy/compose/green.json: intensity 400 operator-set, PUE 1.2):
// seed-payments Δ3600 J = 1.00 Wh → 0.48 g; seed-checkout Δ720 J = 0.20 Wh
// → 0.10 g. Both seeded pods resolve through the kubeletstats join, so all
// energy is attributed — totals 1.20 Wh / 0.58 g, coverage 100.0%, and NO
// (unattributed) row (the hub's coverage-gap bucket holds only join misses;
// synthetic-row behavior is exercised with a stub below).
//
// range=24h wherever seeded energy is asserted: the two counter samples sit
// 60s apart (rebased to ~now−5m) and the hub computes deltas per epoch-aligned
// (end−start)/30 bucket — at the default 15m range the 30s buckets isolate
// each sample and every delta reads 0 (the screen would show the empty state).
// 24h → 2880s buckets co-bucket the pair unless an epoch boundary lands inside
// the 60s gap (~2% of wall-clock instants). Healer: a re-run fixes that; it is
// not a UI regression.
const GREEN_URL = "/green?range=24h";

// The compose stack runs every module and always carries seeded energy, so
// green's empty/error/edge states are simulated by stubbing the hub endpoints
// — the same reasoning as the capabilities stub in modules.spec.ts. Trailing
// `*`: summary/service-map requests carry query params.
const CAPABILITIES = "**/api/v1/capabilities";
const SUMMARY = "**/api/v1/green/summary*";
const BUDGETS = "**/api/v1/green/budgets*";
const SERVICE_MAP = "**/api/v1/service-map*";

const MOCK_WINDOW = { start: "2026-07-22T00:00:00Z", end: "2026-07-23T00:00:00Z" };
const MOCK_FACTORS = {
  gridIntensity: 400,
  intensitySource: "operator-set",
  pue: 1.2,
  dataset: "Ember 2024",
};

test.describe("green dashboard (seeded data)", () => {
  test("renders factors, totals, per-service rows, budget and export", async ({ page }) => {
    await page.goto(GREEN_URL);

    // Factors line echoes the mounted green.json, never a built-in default.
    await expect(page.getByText(/Grid intensity: 400 gCO2e\/kWh/)).toBeVisible();
    await expect(page.getByText(/Source: operator-set/)).toBeVisible();
    await expect(page.getByText(/PUE: 1\.20/)).toBeVisible();

    // The four headline tiles (label + value share a tile div).
    const tile = (label: string) => page.getByText(label, { exact: true }).locator("..");
    await expect(tile("Total energy")).toContainText("1.20 Wh");
    await expect(tile("Total carbon")).toContainText("0.58 g");
    await expect(tile("Avg intensity")).toBeVisible();
    await expect(tile("Attribution coverage")).toContainText("100.0%");

    // Per-service rows carry the fixture energy and its carbon conversion.
    const payments = page.getByRole("row").filter({ hasText: "seed-payments" });
    await expect(payments).toContainText("1.00 Wh");
    await expect(payments).toContainText("0.48 g");
    const checkout = page.getByRole("row").filter({ hasText: "seed-checkout" });
    await expect(checkout).toContainText("0.20 Wh");
    await expect(checkout).toContainText("0.10 g");
    // Full attribution on the seeded stack → no coverage-gap row.
    await expect(page.getByRole("row").filter({ hasText: "(unattributed)" })).toHaveCount(0);

    // The configured monthly budget (deploy/compose/green.json): seeded
    // month-to-date carbon is grams against a 10 kg ceiling.
    await expect(page.getByRole("heading", { name: "Carbon budgets" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "payments-monthly" })).toBeVisible();
    await expect(page.getByText("group: payments")).toBeVisible();
    await expect(page.getByText("On track", { exact: true })).toBeVisible();
    await expect(
      page.getByRole("progressbar", { name: "payments-monthly budget usage" }),
    ).toBeVisible();
    await expect(page.getByText("<1%", { exact: true })).toBeVisible();
    // Alerting runs in compose → no degraded-notifications footnote.
    await expect(page.getByText(/Notifications require the alerting module/)).toHaveCount(0);

    // CSRD export card offers both formats.
    await expect(page.getByRole("heading", { name: "CSRD export" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Download CSV" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Download JSON" })).toBeVisible();
  });

  test("sorts the per-service table by energy and by service", async ({ page }) => {
    await page.goto(GREEN_URL);

    const services = page.locator("tbody tr td:first-child");
    // Heaviest-first is the default ranking.
    await expect(page.getByRole("columnheader", { name: "Energy" })).toHaveAttribute(
      "aria-sort",
      "descending",
    );
    await expect(services).toHaveText(["seed-payments", "seed-checkout"]);

    await page.getByRole("button", { name: "Energy" }).click();
    await expect(page.getByRole("columnheader", { name: "Energy" })).toHaveAttribute(
      "aria-sort",
      "ascending",
    );
    await expect(services).toHaveText(["seed-checkout", "seed-payments"]);

    // A fresh text column starts ascending (mirrors ServicesTable).
    await page.getByRole("button", { name: "Service" }).click();
    await expect(page.getByRole("columnheader", { name: "Service" })).toHaveAttribute(
      "aria-sort",
      "ascending",
    );
    await expect(services).toHaveText(["seed-checkout", "seed-payments"]);
  });

  test("methodology popover states the formula, factor provenance and coverage", async ({ page }) => {
    await page.goto(GREEN_URL);

    // Native <details>; the summary is the disclosure control.
    await page.getByText("Methodology", { exact: true }).click();
    const popover = page.locator("details");
    await expect(popover.getByText("gCO2e = Wh × intensity/1000 × PUE")).toBeVisible();
    await expect(popover.getByText("Factor source")).toBeVisible();
    await expect(popover.getByText("operator-set", { exact: true })).toBeVisible();
    await expect(popover.getByText("Coverage", { exact: true })).toBeVisible();
    await expect(popover.getByText("100.0%")).toBeVisible();
    // The coverage-gap explanation is always stated, even at full attribution.
    await expect(popover.getByText(/could not be mapped to a workload/)).toBeVisible();
  });

  test("downloads the CSRD report as CSV with the methodology block", async ({ page }) => {
    await page.goto(GREEN_URL);

    const responsePromise = page.waitForResponse(
      (r) => r.url().includes("/api/v1/green/report") && r.url().includes("format=csv"),
    );
    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("button", { name: "Download CSV" }).click();

    // The hub serves real CSV and names the file after the period.
    const response = await responsePromise;
    expect(response.headers()["content-type"]).toContain("text/csv");
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/^green-report-\d{8}-\d{8}\.csv$/);

    // Excel BOM, then `# key,value` methodology lines, then the data table.
    const body = readFileSync(await download.path(), "utf8");
    expect(body.startsWith("\ufeff# ")).toBe(true);
    expect(body).toContain("# formula");
    expect(body).toContain("seed-payments");

    // The JSON flavor downloads too (no Content-Disposition → fallback name).
    const jsonDownload = page.waitForEvent("download");
    await page.getByRole("button", { name: "Download JSON" }).click();
    expect((await jsonDownload).suggestedFilename()).toMatch(/\.json$/);
  });
});

test.describe("green dashboard (stubbed states)", () => {
  test("budget cards render warn and exceeded states", async ({ page }) => {
    await page.route(BUDGETS, (route) =>
      route.fulfill({
        json: {
          window: MOCK_WINDOW,
          budgets: [
            {
              name: "checkout-monthly",
              group: "checkout",
              monthlyKgCO2e: 10,
              usedKgCO2e: 8.5,
              projectedKgCO2e: 12.4,
              ratio: 0.85,
              status: "warn",
              burnDown: [
                { time: "2026-07-01T00:00:00Z", kgCO2e: 2.1 },
                { time: "2026-07-08T00:00:00Z", kgCO2e: 4.4 },
                { time: "2026-07-15T00:00:00Z", kgCO2e: 6.6 },
                { time: "2026-07-22T00:00:00Z", kgCO2e: 8.5 },
              ],
            },
            {
              name: "batch-monthly",
              group: "batch",
              monthlyKgCO2e: 5,
              usedKgCO2e: 6,
              projectedKgCO2e: 8.1,
              ratio: 1.2,
              status: "exceeded",
              burnDown: [],
            },
          ],
        },
      }),
    );
    // Budgets render only when the (seeded) summary shows energy.
    await page.goto(GREEN_URL);

    // Budget cards are Card divs; scope by name so the burn-down assertions
    // land on the right card.
    const card = (name: string) => page.locator("div.rounded-xl").filter({ hasText: name });

    const warn = card("checkout-monthly");
    await expect(warn.getByText("Warning", { exact: true })).toBeVisible();
    await expect(
      page.getByRole("progressbar", { name: "checkout-monthly budget usage" }),
    ).toHaveAttribute("aria-valuenow", "85");
    await expect(warn.getByText("12.4 kg")).toBeVisible();
    await expect(warn.getByText(/124\.0% of budget/)).toBeVisible();
    await expect(warn.getByRole("img", { name: "carbon burn-down" })).toBeVisible();

    const exceeded = card("batch-monthly");
    await expect(exceeded.getByText("Exceeded", { exact: true })).toBeVisible();
    await expect(exceeded.getByText("not enough data yet")).toBeVisible();
    await expect(exceeded.getByRole("img", { name: "carbon burn-down" })).toHaveCount(0);
  });

  test("budget card notes the missing alerting module", async ({ page }) => {
    await page.route(CAPABILITIES, (route) =>
      route.fulfill({ json: { version: "test", modules: ["core", "green"] } }),
    );
    await page.goto(GREEN_URL);

    // The seeded payments-monthly budget renders status-only, with the hint.
    await expect(page.getByRole("heading", { name: "payments-monthly" })).toBeVisible();
    await expect(page.getByText(/Notifications require the alerting module/)).toBeVisible();
  });

  test("empty state teaches the RAPL prerequisites", async ({ page }) => {
    await page.route(SUMMARY, (route) =>
      route.fulfill({
        json: {
          window: MOCK_WINDOW,
          factors: MOCK_FACTORS,
          totals: {
            attributedWh: 0,
            measuredWh: 0,
            estimatedWh: 0,
            unattributedWh: 0,
            coverage: 0,
            gco2e: 0,
            nodeCoverage: { known: 0, measured: 0, estimated: 0, absent: 0 },
          },
          services: [],
        },
      }),
    );
    await page.goto("/green");

    await expect(page.getByRole("heading", { name: "No energy measured yet" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Prerequisites" })).toBeVisible();
    await expect(page.getByText("powercap readable in the kernel")).toBeVisible();
    await expect(page.getByText(/--set modules\.green\.enabled=true/)).toBeVisible();
    await expect(page.getByText(/--set sensor\.green\.enabled=true/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Copy Helm command" })).toBeVisible();
    // The TDP-estimation alternative is taught alongside the RAPL checklist.
    await expect(page.getByText(/No RAPL\? Enable TDP estimation instead/)).toBeVisible();
    await expect(page.getByText(/--set sensor\.green\.estimation\.enabled=true/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Copy TDP estimation Helm command" })).toBeVisible();
    // No data is expected on RAPL-less hardware — never an outage message.
    await expect(page.getByText(/Couldn.t reach the hub/)).toHaveCount(0);
  });

  test("hub outage shows the outage message, never the empty state", async ({ page }) => {
    // A 500 is NOT missing hardware: the RAPL checklist here would misdiagnose
    // an outage as no-RAPL and hand the operator a useless Helm command.
    await page.route(SUMMARY, (route) =>
      route.fulfill({ status: 500, json: { error: { message: "boom" } } }),
    );
    await page.goto("/green");

    await expect(
      page.getByText(/Couldn.t reach the hub to read energy & carbon data/),
    ).toBeVisible();
    await expect(page.getByText("No energy measured yet")).toHaveCount(0);
  });

  test("synthetic (other)/(unattributed) rows stay pinned under sorting", async ({ page }) => {
    await page.route(SUMMARY, (route) =>
      route.fulfill({
        json: {
          window: MOCK_WINDOW,
          factors: MOCK_FACTORS,
          totals: {
            attributedWh: 6.5,
            measuredWh: 6.5,
            estimatedWh: 0,
            unattributedWh: 5,
            coverage: 0.565,
            gco2e: 5.52,
            nodeCoverage: { known: 1, measured: 1, estimated: 0, absent: 0 },
          },
          services: [
            { service: "svc-b", wh: 3, gco2e: 1.44, requests: 100, mgCO2ePerRequest: 14.4 },
            { service: "svc-a", wh: 2, gco2e: 0.96, requests: 50, mgCO2ePerRequest: 19.2 },
            { service: "svc-c", wh: 1, gco2e: 0.48, requests: 10, mgCO2ePerRequest: 48 },
            { service: "(other)", wh: 0.5, gco2e: 0.24 },
            { service: "(unattributed)", wh: 5, gco2e: 2.4 },
          ],
        },
      }),
    );
    await page.goto("/green");

    const services = page.locator("tbody tr td:first-child");
    await page.getByRole("button", { name: "Service" }).click();
    await expect(page.getByRole("columnheader", { name: "Service" })).toHaveAttribute(
      "aria-sort",
      "ascending",
    );
    // Real rows sort alphabetically; the roll-ups pin last even though
    // (unattributed) is the heaviest row in the response.
    await expect(services).toHaveText(["svc-a", "svc-b", "svc-c", "(other)", "(unattributed)"]);

    // The coverage-gap row is muted and shows no request-derived numbers.
    const unattr = page.getByRole("row").filter({ hasText: "(unattributed)" });
    await expect(unattr).toHaveClass(/italic/);
    await expect(unattr.getByRole("cell").nth(3)).toHaveText("—");
  });

  test("quality badge and node-coverage panel render a mixed measured/estimated window", async ({
    page,
  }) => {
    await page.route(SUMMARY, (route) =>
      route.fulfill({
        json: {
          window: MOCK_WINDOW,
          factors: MOCK_FACTORS,
          totals: {
            attributedWh: 4,
            measuredWh: 1,
            estimatedWh: 3,
            unattributedWh: 0,
            coverage: 1,
            gco2e: 1.92,
            nodeCoverage: { known: 3, measured: 1, estimated: 1, absent: 1 },
          },
          services: [
            { service: "web", wh: 4, estimatedWh: 3, gco2e: 1.92, requests: 10, mgCO2ePerRequest: 192 },
          ],
        },
      }),
    );
    await page.goto(GREEN_URL);

    // 3 of 4 Wh (75%) is estimated.
    await expect(page.getByText("75% estimated")).toBeVisible();

    // The coverage panel surfaces all four node-tier counts.
    const tile = (label: string) => page.getByText(label, { exact: true }).locator("..");
    await expect(tile("Nodes known")).toContainText("3");
    await expect(tile("Measured")).toContainText("1");
    await expect(tile("Estimated")).toContainText("1");
    await expect(tile("Absent")).toContainText("1");
  });

  test("quality badge is absent when energy is entirely measured", async ({ page }) => {
    await page.route(SUMMARY, (route) =>
      route.fulfill({
        json: {
          window: MOCK_WINDOW,
          factors: MOCK_FACTORS,
          totals: {
            attributedWh: 4,
            measuredWh: 4,
            estimatedWh: 0,
            unattributedWh: 0,
            coverage: 1,
            gco2e: 1.92,
            nodeCoverage: { known: 1, measured: 1, estimated: 0, absent: 0 },
          },
          services: [{ service: "web", wh: 4, gco2e: 1.92, requests: 10, mgCO2ePerRequest: 192 }],
        },
      }),
    );
    await page.goto(GREEN_URL);

    await expect(page.getByText(/estimated/)).toHaveCount(0);
    // A fully-measured, single-tier install has nothing to show in the
    // coverage panel either — it stays hidden rather than showing all-zeros
    // noise.
    await expect(page.getByText("Nodes known", { exact: true })).toHaveCount(0);
  });
});

test.describe("service map carbon overlay", () => {
  test("offers the carbon lens with a legend, round-tripped through the URL", async ({ page }) => {
    // range=24h so the hub's energy stamp is non-zero (see the GREEN_URL note)
    // — wh is omitempty, and without it the toggle must not exist.
    await page.goto("/service-map?range=24h");

    // Honest limitation: the halo colors are drawn on a canvas and cannot be
    // asserted from the DOM. The user-facing contract held here is the toggle,
    // its URL round-trip, and the legend; the tooltip test below proves the
    // per-node energy actually reaches the graph.
    const carbon = page.getByRole("checkbox", { name: "Carbon" });
    const legend = page.getByTestId("map-legend");
    await expect(page.getByRole("checkbox", { name: "Show auxiliary requests" })).toBeVisible();
    await expect(legend.getByText(/halo = gCO2e/)).toHaveCount(0);
    await carbon.check();
    await expect(page).toHaveURL(/carbon=true/);
    await expect(legend.getByText(/halo = gCO2e/)).toBeVisible();

    await carbon.uncheck();
    await expect(page).not.toHaveURL(/[?&]carbon=/);
    await expect(legend.getByText(/halo = gCO2e/)).toHaveCount(0);
  });

  test("node tooltip reports Wh and gCO2e under the carbon lens", async ({ page }) => {
    // A single deterministic node pins the tooltip content and keeps the
    // layout predictable (a lone node lands centred) — same stub-to-simulate
    // reasoning as above.
    await page.route(SERVICE_MAP, (route) =>
      route.fulfill({
        json: {
          services: [
            {
              name: "seed-payments",
              spanCount: 120,
              ratePerSec: 2,
              errorRate: 0,
              p50Ms: 4,
              p95Ms: 9,
              p99Ms: 15,
              wh: 1.0,
              gco2e: 0.48,
            },
          ],
          edges: [],
        },
      }),
    );
    await page.goto("/service-map?carbon=true");
    await expect(page.getByText(/click a node for its traces/)).toBeVisible();

    // Canvas hover is positional: sweep a 3×3 grid around the centre until the
    // node's mouseover fires. FLAKE-FLAGGED — healer: if this cannot be kept
    // stable, downgrade to the toggle+legend contract (previous test) and note
    // the tooltip as manually verified, per the scenario plan.
    const tooltip = "seed-payments · 1.0 Wh · 0.48 gCO2e";
    const canvas = page.locator("canvas").first();
    await expect(canvas).toBeVisible();
    const box = (await canvas.boundingBox())!;
    await expect
      .poll(
        async () => {
          for (const dy of [0, -30, 30]) {
            for (const dx of [0, -30, 30]) {
              await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy);
              if (await page.getByText(tooltip, { exact: true }).count()) return true;
            }
          }
          return false;
        },
        { timeout: 15_000 },
      )
      .toBe(true);
    await expect(page.getByText(tooltip, { exact: true })).toBeVisible();
  });

  test("carbon lens is absent when the green module is off", async ({ page }) => {
    await page.route(CAPABILITIES, (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );
    // A stale ?carbon=true bookmark must not resurrect the lens.
    await page.goto("/service-map?carbon=true&range=24h");

    await expect(page.getByText(/click a node for its traces/)).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Carbon" })).toHaveCount(0);
    await expect(page.getByText(/Carbon lens on/)).toHaveCount(0);
  });

  test("carbon lens is absent when the map carries no energy", async ({ page }) => {
    // Green active (real capabilities) but a map without wh/gco2e — e.g. a
    // RAPL-less cluster: the overlay must not be offered.
    await page.route(SERVICE_MAP, (route) =>
      route.fulfill({
        json: {
          services: [
            { name: "svc-a", spanCount: 10, ratePerSec: 1, errorRate: 0, p50Ms: 4, p95Ms: 9, p99Ms: 15 },
          ],
          edges: [],
        },
      }),
    );
    await page.goto("/service-map");

    await expect(page.getByText(/click a node for its traces/)).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Carbon" })).toHaveCount(0);
    await expect(page.getByRole("checkbox", { name: "Show auxiliary requests" })).toBeVisible();
  });
});
