import { test, expect } from "@playwright/test";

// The compose seed has traces+logs but no kubeletstats metrics, so the Nodes
// screen must land on its teaching empty state (not an error).
test.describe("nodes screen", () => {
  test("renders the empty state without metric data", async ({ page }) => {
    await page.goto("/nodes");
    await expect(page.getByText("No node metrics yet")).toBeVisible();
    await expect(page.getByText(/sensor DaemonSet/)).toBeVisible();
  });
});
