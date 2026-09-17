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
    const resolutions = services.includes("unknown-service")
      ? [{ service: "unknown-service", proxiesUnavailable: "no workload could be resolved for mesh logs" }]
      : [];
    return route.fulfill({ json: { logs: rows, resolutions, nextCursor: "" } });
  });
}

function contrast(foreground: string, background: string) {
  const luminance = (hex: string) => {
    const channels = hex.match(/[\da-f]{2}/gi)!.map((value) => parseInt(value, 16) / 255);
    const linear = channels.map((value) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
    return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
  };
  const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
  return (lighter + 0.05) / (darker + 0.05);
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

  test("uses the slate palette in dark mode with readable body text", async ({ page }) => {
    await stubLogs(page);
    await page.goto("/logs");
    await page.getByRole("button", { name: "Switch to dark theme" }).click();
    const palette = await page.evaluate(() => {
      const root = getComputedStyle(document.documentElement);
      const explorer = document.body.appendChild(document.createElement("div"));
      explorer.className = "explorer-canvas";
      const xray = document.body.appendChild(document.createElement("div"));
      xray.className = "xray-surface";
      const values = {
        page: root.getPropertyValue("--color-base-100").trim(),
        panel: root.getPropertyValue("--color-base-200").trim(),
        field: root.getPropertyValue("--color-base-300").trim(),
        text: root.getPropertyValue("--color-base-content").trim(),
        accent: root.getPropertyValue("--color-primary").trim(),
        explorer: getComputedStyle(explorer).getPropertyValue("--color-base-100").trim(),
        xray: getComputedStyle(xray).getPropertyValue("--color-base-100").trim(),
      };
      explorer.remove();
      xray.remove();
      return values;
    });
    expect(palette).toEqual({
      page: "#111827",
      panel: "#1e293b",
      field: "#273449",
      text: "#e2e8f0",
      accent: "#93c5fd",
      explorer: "#111827",
      xray: "#111827",
    });
    expect(contrast(palette.text, palette.page)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(palette.text, palette.panel)).toBeGreaterThanOrEqual(4.5);
  });
});
