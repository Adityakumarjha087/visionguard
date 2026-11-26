// Reproducible README screenshots against a running `docker compose up` stack.
//   node scripts/screenshots.mjs        (uses Playwright from frontend/node_modules)
// Env: UI (default http://localhost:20900), API (default http://localhost:20901), GRAFANA (http://localhost:20921)
import { createRequire } from "node:module";
import { mkdirSync } from "node:fs";

const require = createRequire(new URL("../frontend/package.json", import.meta.url));
const { chromium } = require("@playwright/test");

const UI = process.env.UI ?? "http://localhost:20900";
const API = process.env.API ?? "http://localhost:20901";
const GRAFANA = process.env.GRAFANA ?? "http://localhost:20921";
const OUT = new URL("../docs/screenshots/", import.meta.url).pathname;
mkdirSync(OUT, { recursive: true });

const login = async (page) => {
  await page.goto(`${UI}/login`);
  await page.getByLabel("Email").fill("operator@visionguard.dev");
  await page.getByLabel("Password").fill("operator12345");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL(/\/dashboard/);
};

const settle = async (page, ms = 1500) => {
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(ms);
};

// Wait until frames flow and the synthetic scene is busy (the 60 s loop has empty stretches).
const waitForBusyScene = (page) =>
  page.waitForFunction(
    () => {
      const fps = Number(document.querySelector('[data-testid="fps-counter"]')?.textContent);
      const m = document.body.innerText.match(/(\d+) objects/);
      return fps > 0 && m && Number(m[1]) >= 8;
    },
    null,
    { timeout: 90000, polling: 200 },
  );

const shot = async (page, name) => {
  // Transient alert toasts would cover the header; they are shown on the dashboard feed instead.
  await page.addStyleTag({ content: "[data-sonner-toaster]{display:none !important}" });
  await page.screenshot({ path: `${OUT}${name}.png` });
  console.log(`saved ${name}.png`);
};

const token = await fetch(`${API}/api/v1/auth/login`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ email: "operator@visionguard.dev", password: "operator12345" }),
}).then((r) => r.json()).then((j) => j.access_token);
const cams = await fetch(`${API}/api/v1/cameras`, { headers: { Authorization: `Bearer ${token}` } }).then((r) => r.json());
const warehouse = cams.find((c) => c.name === "Warehouse Floor (sample)");

const browser = await chromium.launch();
for (const scheme of ["light", "dark"]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: scheme, deviceScaleFactor: 1 });
  const page = await ctx.newPage();
  await login(page);

  if (scheme === "dark") {
    // Dark mode: live camera view (boxes, track ids, fps overlay).
    await page.goto(`${UI}/cameras/${warehouse.id}`);
    await waitForBusyScene(page);
    await shot(page, "live-view-dark");
    await ctx.close();
    continue;
  }

  // 1. Dashboard with KPIs, camera tiles and live alert feed.
  await settle(page, 4000);
  await shot(page, "dashboard");

  // 2. Live view with canvas overlay.
  await page.goto(`${UI}/cameras/${warehouse.id}`);
  await waitForBusyScene(page);
  await shot(page, "live-view");

  // 3. Zone editor with a polygon being drawn over the still frame.
  await page.getByRole("tab", { name: /Zones/ }).click();
  const canvas = page.getByTestId("zone-canvas");
  await canvas.waitFor();
  await settle(page, 1500);
  const box = await canvas.boundingBox();
  for (const [x, y] of [[0.08, 0.12], [0.3, 0.1], [0.34, 0.38], [0.12, 0.42]]) {
    await page.mouse.click(box.x + box.width * x, box.y + box.height * y);
    await page.waitForTimeout(150);
  }
  await settle(page, 500);
  await shot(page, "zone-editor");

  // 4. Incident log with an incident opened (snapshot + AI description).
  await page.goto(`${UI}/incidents`);
  await page.getByTestId("incident-row").first().waitFor();
  await settle(page, 1000);
  const rows = page.getByTestId("incident-row");
  const n = await rows.count();
  for (let i = 0; i < n; i++) {
    // Prefer a live incident (has a snapshot) over seeded history.
    await rows.nth(i).click();
    const dialog = page.getByRole("dialog");
    await dialog.waitFor();
    await page.waitForTimeout(800);
    // Prefer a live incident whose snapshot, clip and AI description are all ready.
    const ready = (await dialog.locator("img").count()) > 0 && (await page.getByTestId("incident-clip").count()) > 0;
    if (ready) break;
    await page.keyboard.press("Escape");
    await page.waitForTimeout(300);
  }
  await page.getByTestId("incident-description").waitFor();
  await page.getByTestId("incident-description").scrollIntoViewIfNeeded();
  await settle(page, 2000);
  await shot(page, "incident-detail");
  await page.keyboard.press("Escape");

  // 5. Analytics: counts, heatmap, crossings.
  await page.goto(`${UI}/analytics`);
  await page.getByTestId("heatmap-canvas").waitFor({ timeout: 30000 });
  await settle(page, 3000);
  await shot(page, "analytics");

  // 6. Model management with benchmarks.
  await page.goto(`${UI}/models`);
  await page.getByTestId("model-row").first().waitFor();
  await settle(page, 1500);
  await shot(page, "models");

  // 7. Grafana system metrics (anonymous viewer access).
  await page.goto(`${GRAFANA}/d/visionguard-overview?orgId=1&refresh=&from=now-15m&to=now&kiosk`);
  await settle(page, 6000);
  await shot(page, "grafana");
  await ctx.close();
}
await browser.close();
