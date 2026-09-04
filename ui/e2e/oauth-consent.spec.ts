import { test, expect } from "@playwright/test";

// The consent screen's job is disclosure, so that is what this asserts.
//
// The API is stubbed: driving a real authorization flow needs a registered
// client and a code exchange, which the Go e2e suite covers end to end. What
// lives only here is what a person is actually shown before they approve —
// and the risk is that a redesign quietly drops it.
const VIEW = {
  clientId: "c-123",
  clientName: "Some Connector",
  clientVerified: false,
  redirectHost: "app.example.com",
  firstUse: true,
  scopes: ["mcp:read"],
  projects: ["prod", "payments"],
  defaultProject: "prod",
  resource: "https://obs.example.com/mcp",
};

const CONSENT_URL = "/oauth/consent?client_id=c-123&redirect_uri=https%3A%2F%2Fapp.example.com%2Fcb";

test.describe("oauth consent", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/auth/oauth/consent*", (route) => {
      if (route.request().method() === "GET") return route.fulfill({ json: VIEW });
      return route.fulfill({ json: { redirect: "https://app.example.com/cb?code=abc&state=xyz" } });
    });
  });

  test("says that approving sends log bodies out of the installation", async ({ page }) => {
    await page.goto(CONSENT_URL);

    const card = page.getByTestId("oauth-consent");
    await expect(card).toBeVisible();
    await expect(page.getByTestId("consent-client")).toHaveText("Some Connector");

    // The sentence the whole screen exists for. It must be visible on arrival,
    // not behind a disclosure toggle.
    const disclosure = page.getByTestId("consent-disclosure");
    await expect(disclosure).toBeVisible();
    await expect(disclosure).toContainText(/out of your cluster/i);
    await expect(disclosure).toContainText(/log bodies/i);
    await expect(disclosure).toContainText(/model provider/i);
    // And that the reading is logged.
    await expect(disclosure).toContainText(/recorded with your name/i);
  });

  test("presents the client's own name as unverified, and names the host", async ({ page }) => {
    await page.goto(CONSENT_URL);

    // Registration is unauthenticated, so the name is attacker-controlled.
    const caveat = page.getByTestId("consent-unverified");
    await expect(caveat).toContainText(/has not been verified/i);
    // The host is the one fact a reader can check.
    await expect(caveat).toContainText("app.example.com");
  });

  test("scopes access to one project, chosen from the reader's own", async ({ page }) => {
    await page.goto(CONSENT_URL);

    const select = page.getByTestId("consent-project");
    await expect(select).toHaveValue("prod");
    await expect(select.locator("option")).toHaveCount(2);
  });

  test("cancel reports the refusal instead of doing nothing", async ({ page }) => {
    let posted: unknown = null;
    await page.route("**/api/v1/auth/oauth/consent*", (route) => {
      if (route.request().method() === "GET") return route.fulfill({ json: VIEW });
      posted = route.request().postDataJSON();
      return route.fulfill({ json: { redirect: "https://app.example.com/cb?error=access_denied" } });
    });
    await page.goto(CONSENT_URL);
    await page.getByRole("button", { name: "Cancel" }).click();
    await expect.poll(() => posted).toEqual({ approve: false, project: "prod" });
  });

  test("approve sends the chosen project", async ({ page }) => {
    let posted: unknown = null;
    await page.route("**/api/v1/auth/oauth/consent*", (route) => {
      if (route.request().method() === "GET") return route.fulfill({ json: VIEW });
      posted = route.request().postDataJSON();
      return route.fulfill({ json: { redirect: "https://app.example.com/cb?code=abc" } });
    });
    await page.goto(CONSENT_URL);
    await page.getByTestId("consent-project").selectOption("payments");
    await page.getByTestId("consent-approve").click();
    await expect.poll(() => posted).toEqual({ approve: true, project: "payments" });
  });
});
