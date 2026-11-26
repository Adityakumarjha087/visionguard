import { expect, test, type Page } from "@playwright/test";

/**
 * Runs against the full docker compose stack (seeded DB + inference on the sample video).
 *   E2E_BASE_URL=http://localhost:20900 npx playwright test
 */

const SAMPLE_CAMERA = "Warehouse Floor (sample)";

async function login(page: Page, email: string, password: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/dashboard/);
}

test.describe("VisionGuard", () => {
  test("operator: dashboard -> live camera overlay -> incidents with AI description", async ({ page }) => {
    await login(page, "operator@visionguard.dev", "operator12345");

    // dashboard
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
    await expect(page.getByTestId("kpi-cameras-online")).toBeVisible();

    // cameras list shows the seeded sample camera
    await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Cameras" }).click();
    await expect(page.getByRole("heading", { name: "Cameras" })).toBeVisible();
    const row = page.getByTestId("camera-row").filter({ hasText: SAMPLE_CAMERA });
    await expect(row).toBeVisible();
    await expect(page.getByRole("button", { name: "Add camera" })).toBeVisible();

    // detail page: live overlay must report FPS > 0 (frames arriving over the WebSocket)
    await row.getByRole("link", { name: SAMPLE_CAMERA }).click();
    await expect(page.getByRole("heading", { name: SAMPLE_CAMERA })).toBeVisible();
    await expect(page.getByTestId("live-canvas")).toBeAttached();
    await expect
      .poll(async () => Number(await page.getByTestId("fps-counter").textContent()), { timeout: 60_000, intervals: [1_000] })
      .toBeGreaterThan(0);

    // incidents: at least one incident appears within 60s
    await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Incidents" }).click();
    await expect(page.getByRole("heading", { name: "Incidents" })).toBeVisible();
    await expect(page.getByTestId("incident-row").first()).toBeVisible({ timeout: 60_000 });

    // open the first incident; the AI description is filled (possibly after an incident_update)
    await page.getByTestId("incident-row").first().click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await expect(page.getByTestId("incident-description")).not.toBeEmpty({ timeout: 60_000 });
  });

  test("viewer: no Add camera button", async ({ page }) => {
    await login(page, "viewer@visionguard.dev", "viewer12345");
    await page.goto("/cameras");
    await expect(page.getByRole("heading", { name: "Cameras" })).toBeVisible();
    await expect(page.getByTestId("camera-row").first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Add camera" })).toHaveCount(0);
    // viewers do not see the admin-only Users nav item either
    await expect(page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Users" })).toHaveCount(0);
  });
});
