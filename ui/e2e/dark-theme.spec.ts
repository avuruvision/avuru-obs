import { test, expect } from "@playwright/test";

// The dark theme is slate and blue, not forest and lime: the page, the
// panels, the fields, the map canvas and the X-Ray scene all read from one
// palette, and every text token on it clears 4.5:1 — including the muted
// one, which used to be an opacity and therefore failed on the light theme.
function contrast(foreground: string, background: string) {
  const luminance = (hex: string) => {
    const channels = hex.match(/[\da-f]{2}/gi)!.map((value) => parseInt(value, 16) / 255);
    const linear = channels.map((value) => (value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4));
    return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
  };
  const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
  return (lighter + 0.05) / (darker + 0.05);
}

async function palette(page: import("@playwright/test").Page) {
  return page.evaluate(() => {
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
      muted: root.getPropertyValue("--color-base-content-muted").trim(),
      accent: root.getPropertyValue("--color-primary").trim(),
      explorer: getComputedStyle(explorer).getPropertyValue("--color-base-100").trim(),
      xray: getComputedStyle(xray).getPropertyValue("--color-base-100").trim(),
    };
    explorer.remove();
    xray.remove();
    return values;
  });
}

test.describe("slate dark theme", () => {
  test("uses the slate palette in dark mode on the page, the map and X-Ray", async ({ page }) => {
    await page.goto("/logs");
    await page.getByRole("button", { name: "Switch to dark theme" }).click();
    const dark = await palette(page);
    expect(dark).toEqual({
      page: "#111827",
      panel: "#1e293b",
      field: "#273449",
      text: "#e2e8f0",
      muted: "#a8b5c7",
      accent: "#93c5fd",
      explorer: "#111827",
      xray: "#111827",
    });
    for (const surface of [dark.page, dark.panel, dark.field]) {
      expect(contrast(dark.text, surface)).toBeGreaterThanOrEqual(4.5);
      expect(contrast(dark.muted, surface)).toBeGreaterThanOrEqual(4.5);
    }
  });

  test("keeps the light theme, with readable muted text", async ({ page }) => {
    await page.goto("/logs");
    const light = await palette(page);
    expect(light.page).toBe("#edf2e9");
    expect(light.accent).toBe("#1c4b36");
    expect(light.muted).toBe("#4f6457");
    for (const surface of [light.page, light.panel, light.field]) {
      expect(contrast(light.text, surface)).toBeGreaterThanOrEqual(4.5);
      expect(contrast(light.muted, surface)).toBeGreaterThanOrEqual(4.5);
    }
  });
});
