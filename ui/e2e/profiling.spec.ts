import { test, expect } from "@playwright/test";

// The compose seed has no profiling samples (the profiler runs in k8s only),
// so the Profiling screen must land on its teaching empty state.
test.describe("profiling screen", () => {
  test("renders the empty state without profile data", async ({ page }) => {
    await page.goto("/profiling");
    await expect(page.getByText("No profiling data yet")).toBeVisible();
    await expect(page.getByText(/profiler container/)).toBeVisible();
  });
});
