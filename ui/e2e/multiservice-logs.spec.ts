import { test, expect, type Page } from "@playwright/test";

const log = (service: string, source: string, body: string) => ({
  timestamp: "2026-09-17T10:00:00Z",
  severity: "INFO",
  service,
  source,
  body,
  traceId: "",
  spanId: "",
});

async function stubLogs(page: Page) {
  await page.route("**/api/v1/logs/services*", (route) =>
    route.fulfill({
      json: {
        services: ["checkout-api", "inventory-api", "log-only"],
        workloads: ["ops/worker", "shop/checkout"],
      },
    }),
  );
  await page.route(/\/api\/v1\/logs(?:\?.*)?$/, (route) => {
    const url = new URL(route.request().url());
    const services = url.searchParams.getAll("service");
    const rows = [
      log("checkout-api", "application", "checkout ready"),
      log("inventory-api", "application", "inventory ready"),
      log("ztunnel", "ztunnel", "forwarded checkout-api inventory-api"),
    ].filter((row) => services.length === 0 || services.includes(row.service) || row.source !== "application");
    const resolutions = [
      ...(services.includes("unknown-service")
        ? [{ service: "unknown-service", proxiesUnavailable: "no workload could be resolved for mesh logs" }]
        : []),
      ...(services.includes("inventory-api")
        ? [{ service: "inventory-api", namespace: "shop", workload: "inventory", proxiesMatchedBy: "pods are matched by name: mesh-config is off, so the workload's pods are not known" }]
        : []),
    ];
    return route.fulfill({ json: { logs: rows, resolutions, nextCursor: "" } });
  });
}

test.describe("multiservice log explorer", () => {
  test("selects several services and restores the merged view from the URL", async ({ page }) => {
    await stubLogs(page);
    await page.goto("/logs");

    const picker = page.getByRole("combobox", { name: "Select log services" });
    await picker.fill("checkout");
    await page.getByRole("option", { name: "checkout-api" }).click();
    await picker.fill("inventory");
    await page.getByRole("option", { name: "inventory-api" }).click();

    await expect(page).toHaveURL(/services=checkout-api%2Cinventory-api/);
    await expect(page.getByRole("columnheader", { name: "Source" })).toBeVisible();
    await expect(page.getByText("checkout ready")).toBeVisible();
    await expect(page.getByText("inventory ready")).toBeVisible();
    await expect(page.getByText("forwarded checkout-api inventory-api")).toHaveCount(1);
    await expect(page.getByRole("checkbox", { name: "Application" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "ztunnel" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "waypoint" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Other" })).toBeChecked();

    await page.reload();
    await expect(page.getByRole("button", { name: "Remove checkout-api" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Remove inventory-api" })).toBeVisible();
  });

  test("switches to independent service panels and asks for a selection when empty", async ({ page }) => {
    await stubLogs(page);
    await page.goto("/logs");
    await page.getByRole("button", { name: "Service panels" }).click();
    await expect(page).toHaveURL(/display=panels/);
    await expect(page.getByText("Choose one or more services to open panels")).toBeVisible();

    const picker = page.getByRole("combobox", { name: "Select log services" });
    await picker.fill("checkout");
    await page.getByRole("option", { name: "checkout-api" }).click();
    await expect(page.getByRole("region", { name: "Logs for checkout-api" })).toBeVisible();
  });

  test("keeps panel pagination and errors independent", async ({ page }) => {
    await page.route("**/api/v1/logs/services*", (route) => route.fulfill({ json: { services: [], workloads: [] } }));
    await page.route(/\/api\/v1\/logs(?:\?.*)?$/, (route) => {
      const url = new URL(route.request().url());
      const service = url.searchParams.get("service");
      if (service === "inventory-api") return route.fulfill({ status: 500, json: { error: "boom" } });
      const second = url.searchParams.has("cursor");
      return route.fulfill({
        json: {
          logs: [log("checkout-api", "application", second ? "checkout page two" : "checkout page one")],
          resolutions: [],
          nextCursor: second ? "" : "next-checkout",
        },
      });
    });
    await page.goto("/logs?services=checkout-api%2Cinventory-api&display=panels");
    const checkout = page.getByRole("region", { name: "Logs for checkout-api" });
    const inventory = page.getByRole("region", { name: "Logs for inventory-api" });
    await expect(checkout).toContainText("checkout page one");
    await expect(checkout).toContainText("checkout page two");
    await expect(inventory).toContainText("Unable to load this panel.");
  });

  test("loads every panel with at most four requests in flight", async ({ page }) => {
    await page.route("**/api/v1/logs/services*", (route) => route.fulfill({ json: { services: [], workloads: [] } }));
    let active = 0;
    let maximum = 0;
    const seen = new Set<string>();
    await page.route(/\/api\/v1\/logs(?:\?.*)?$/, async (route) => {
      const service = new URL(route.request().url()).searchParams.get("service") ?? "";
      active += 1;
      maximum = Math.max(maximum, active);
      seen.add(service);
      await new Promise((resolve) => setTimeout(resolve, 100));
      await route.fulfill({ json: { logs: [log(service, "application", `${service} line`)], resolutions: [] } });
      active -= 1;
    });
    await page.goto("/logs?services=one%2Ctwo%2Cthree%2Cfour%2Cfive&display=panels");
    await expect(page.getByRole("region", { name: "Logs for five" })).toContainText("five line");
    expect([...seen].sort()).toEqual(["five", "four", "one", "three", "two"]);
    expect(maximum).toBeLessThanOrEqual(4);
  });

  test("uses a compact workload resolution token on later pages", async ({ page }) => {
    await page.route("**/api/v1/logs/services*", (route) => route.fulfill({ json: { services: [], workloads: [] } }));
    let stableTokenSeen = false;
    await page.route(/\/api\/v1\/logs(?:\?.*)?$/, (route) => {
      const url = new URL(route.request().url());
      const second = url.searchParams.has("cursor");
      if (second) {
        stableTokenSeen = url.searchParams.get("resolution") === "stable-resolution-token";
      }
      return route.fulfill({
        json: {
          logs: [log("ztunnel", "ztunnel", second ? "workload page two" : "workload page one")],
          resolutions: [{
            namespace: "shop",
            workload: "checkout",
          }],
          resolutionToken: "stable-resolution-token",
          nextCursor: second ? "" : "next-workload",
        },
      });
    });
    await page.goto("/logs?workloads=shop%2Fcheckout&display=panels");
    const panel = page.getByRole("region", { name: "Logs for shop/checkout" });
    await expect(panel).toContainText("workload page two");
    expect(stableTokenSeen).toBe(true);
  });

  test("accepts an exact service and explains unavailable mesh sources", async ({ page }) => {
    await stubLogs(page);
    await page.goto("/logs");
    const picker = page.getByRole("combobox", { name: "Select log services" });
    await picker.fill("unknown-service");
    await picker.press("Enter");
    await expect(page).toHaveURL(/services=unknown-service/);
    await expect(page.getByRole("status")).toContainText("no workload could be resolved for mesh logs");
  });

  test("offers the same explorer as a service mesh tab", async ({ page }) => {
    await stubLogs(page);
    await page.goto("/mesh?view=logs&workloads=shop%2Fcheckout");
    await expect(page.getByRole("tab", { name: "Logs", exact: true })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByTestId("mesh-log-explorer")).toBeVisible();
    await expect(page.getByRole("button", { name: "Remove checkout" })).toBeVisible();
  });

  test("selects with the keyboard, drops a chip, and clears every filter", async ({ page }) => {
    await stubLogs(page);
    await page.goto("/logs?severity=ERROR&sources=app");

    // "checkout" matches the checkout-api service and then the shop/checkout
    // workload. The first suggestion is already highlighted, so ArrowDown
    // moves to the second and Enter takes it — no mouse.
    const picker = page.getByRole("combobox", { name: "Select log services" });
    await picker.fill("checkout");
    await picker.press("ArrowDown");
    await picker.press("Enter");
    await expect(page).toHaveURL(/workloads=shop%2Fcheckout/);
    await expect(page).not.toHaveURL(/services=/);
    await picker.press("Escape");
    await expect(page.getByRole("listbox", { name: "Select log services" })).toHaveCount(0);

    // The chip's remove button takes the workload out of the selection.
    await page.getByRole("button", { name: "Remove checkout" }).click();
    await expect(page).not.toHaveURL(/workloads=/);
    await expect(page.getByRole("button", { name: "Remove checkout" })).toHaveCount(0);

    // Clear resets sources, severity and search in one go, and the display
    // choice is not a filter, so it stays.
    await page.getByRole("button", { name: "Service panels" }).click();
    await page.getByRole("button", { name: "Clear" }).click();
    await expect(page).toHaveURL(/display=panels/);
    await expect(page).not.toHaveURL(/severity=|sources=|services=|workloads=/);
    await expect(page.getByRole("checkbox", { name: "waypoint" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Other" })).toBeChecked();
  });

  test("keeps the source choice in the URL and sends it to the hub", async ({ page }) => {
    await stubLogs(page);
    const asked: string[] = [];
    page.on("request", (req) => {
      if (/\/api\/v1\/logs\?/.test(req.url())) asked.push(req.url());
    });
    await page.goto("/logs");
    await page.getByRole("checkbox", { name: "waypoint" }).uncheck();
    await expect(page).toHaveURL(/sources=app%2Cztunnel%2Cother/);
    await expect.poll(() => asked.at(-1) ?? "").toContain("source=app%2Cztunnel%2Cother");

    await page.reload();
    await expect(page.getByRole("checkbox", { name: "waypoint" })).not.toBeChecked();
    await expect(page.getByRole("checkbox", { name: "ztunnel" })).toBeChecked();
    // The last source standing cannot be unchecked: an empty set is not a filter.
    await page.getByRole("checkbox", { name: "ztunnel" }).uncheck();
    await page.getByRole("checkbox", { name: "Other" }).uncheck();
    await expect(page.getByRole("checkbox", { name: "Application" })).toBeDisabled();
  });

  test("says how proxy lines were matched when pods were not known", async ({ page }) => {
    await stubLogs(page);
    await page.goto("/logs?services=inventory-api");
    await expect(page.getByRole("status")).toContainText("inventory-api: pods are matched by name: mesh-config is off");
  });

  test("keeps its search out of the mesh page's proxy filter", async ({ page }) => {
    await stubLogs(page);
    // One proxy, so the Proxies tab renders its filter box rather than the
    // empty state.
    await page.route("**/api/v1/mesh/proxies*", (route) =>
      route.fulfill({
        json: {
          proxies: [{
            name: "ztunnel-abc",
            namespace: "istio-system",
            role: "ztunnel",
            ratePerSec: 1,
            errorRate: 0,
            p50Ms: 1,
            p95Ms: 2,
            callsIn: 10,
            callsOut: 10,
          }],
        },
      }),
    );
    await page.goto("/mesh?view=logs");
    const search = page.getByRole("searchbox", { name: "Search log message" });
    await search.fill("forwarded");
    await search.press("Enter");
    await expect(page).toHaveURL(/lq=forwarded/);
    await expect(page).not.toHaveURL(/[?&]q=/);

    await page.getByRole("tab", { name: "Proxies", exact: true }).click();
    await expect(page.getByRole("searchbox", { name: "Filter proxies" })).toHaveValue("");
  });

  test("folds, widens and follows each panel on its own", async ({ page }) => {
    await page.setViewportSize({ width: 1400, height: 900 });
    await page.route("**/api/v1/logs/services*", (route) => route.fulfill({ json: { services: [], workloads: [] } }));
    const firstPages: Record<string, number> = {};
    await page.route(/\/api\/v1\/logs(?:\?.*)?$/, (route) => {
      const url = new URL(route.request().url());
      const service = url.searchParams.get("service") ?? "";
      if (!url.searchParams.has("cursor")) firstPages[service] = (firstPages[service] ?? 0) + 1;
      return route.fulfill({ json: { logs: [log(service, "application", `${service} ready`)], resolutions: [], nextCursor: "" } });
    });
    await page.goto("/logs?services=checkout-api%2Cinventory-api&display=panels");
    const checkout = page.getByRole("region", { name: "Logs for checkout-api" });
    const inventory = page.getByRole("region", { name: "Logs for inventory-api" });
    await expect(checkout.getByText("checkout-api ready")).toBeVisible();

    // Folding one panel leaves the other open; the layout is not a filter, so
    // the URL does not change.
    await checkout.getByRole("button", { name: "Collapse checkout-api" }).click();
    await expect(checkout.getByText("checkout-api ready")).toBeHidden();
    await expect(inventory.getByText("inventory-api ready")).toBeVisible();
    await expect(page).toHaveURL(/services=checkout-api%2Cinventory-api&display=panels$/);
    await checkout.getByRole("button", { name: "Expand checkout-api" }).click();
    await expect(checkout.getByText("checkout-api ready")).toBeVisible();

    await page.getByRole("button", { name: "Collapse all" }).click();
    await expect(checkout.getByText("checkout-api ready")).toBeHidden();
    await expect(inventory.getByText("inventory-api ready")).toBeHidden();
    await page.getByRole("button", { name: "Expand all" }).click();
    await expect(inventory.getByText("inventory-api ready")).toBeVisible();

    // A widened panel spans the row; the other keeps its half.
    const widen = checkout.getByRole("button", { name: "Widen checkout-api" });
    await widen.click();
    await expect(checkout.getByRole("button", { name: "Narrow checkout-api" })).toHaveAttribute("aria-pressed", "true");
    const wide = await checkout.boundingBox();
    const half = await inventory.boundingBox();
    expect(wide!.width).toBeGreaterThan(half!.width * 1.5);

    // Following polls the newest page of one panel, and only that one.
    const before = firstPages["inventory-api"];
    await checkout.getByRole("button", { name: "Follow" }).click();
    await expect(checkout.getByRole("button", { name: "Follow" })).toHaveAttribute("aria-pressed", "true");
    await expect(checkout.getByRole("status")).toContainText("Live");
    await expect.poll(() => firstPages["checkout-api"], { timeout: 30_000 }).toBeGreaterThanOrEqual(3);
    expect(firstPages["inventory-api"]).toBe(before);
    await checkout.getByRole("button", { name: "Follow" }).click();
    await expect(checkout.getByRole("status")).toHaveCount(0);
  });

  test("scrolls a long panel inside its own box", async ({ page }) => {
    await page.setViewportSize({ width: 1400, height: 900 });
    await page.route("**/api/v1/logs/services*", (route) => route.fulfill({ json: { services: [], workloads: [] } }));
    await page.route(/\/api\/v1\/logs(?:\?.*)?$/, (route) => {
      const second = new URL(route.request().url()).searchParams.has("cursor");
      const offset = second ? 100 : 0;
      return route.fulfill({
        json: {
          logs: Array.from({ length: 100 }, (_, i) => log("checkout-api", "application", `checkout line ${offset + i + 1}`)),
          resolutions: [],
          nextCursor: second ? "" : "next-page",
        },
      });
    });
    await page.goto("/logs?services=checkout-api&display=panels");
    const checkout = page.getByRole("region", { name: "Logs for checkout-api" });
    await expect(checkout.getByText("checkout line 1", { exact: true })).toBeVisible();

    // The panel is capped: the page does not grow with the lines.
    const box = checkout.getByTestId("log-scroll");
    expect(await box.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
    expect((await checkout.boundingBox())!.height).toBeLessThan(900);

    // Scrolling to the bottom of the box loads the next page and offers the
    // way back up.
    await box.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    await expect(checkout.getByText("checkout line 200", { exact: true })).toBeAttached();
    await checkout.getByRole("button", { name: "Back to top" }).click();
    await expect.poll(() => box.evaluate((el) => el.scrollTop)).toBeLessThan(5);
    await expect(checkout).toContainText("200 lines");
  });
});
