import { test, expect } from "@playwright/test";
const user = process.env.E2E_USER, password = process.env.E2E_PASSWORD;
test.use({ ignoreHTTPSErrors: true });
test("shots", async ({ page }) => {
  test.setTimeout(120000);
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(user);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto("/settings/keys/new");
  await page.locator('input[name="name"]').fill("laptop-with-a-quite-long-name");
  await page.getByRole("button", { name: "Create key" }).click();
  await page.goto("/settings/keys/new");
  await page.locator('input[name="name"]').fill("old key");
  await page.getByRole("button", { name: "Create key" }).click();
  await page.goto("/settings/keys");
  const row = page.locator("tr", { hasText: "old key" });
  await row.getByRole("button", { name: /Actions for/ }).click();
  await page.getByRole("menuitem", { name: "Revoke…" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Revoke key" }).click();
  const pages = ["/settings/keys", "/settings/keys?show=all", "/admin/audit", "/admin/users", "/settings/integrations", "/settings/notifications", "/admin/settings", "/admin/secrets", "/settings/keys/new"];
  for (const [w, h] of [[1280, 900], [1120, 900], [390, 800]]) {
    await page.setViewportSize({ width: w, height: h });
    for (const p of pages) {
      await page.goto(p);
      await page.waitForTimeout(400);
      const sw = await page.evaluate(() => [document.documentElement.scrollWidth, window.innerWidth, [...document.querySelectorAll(".pb-tablescroll")].map(e => e.scrollWidth - e.clientWidth)]);
      console.log(w, p, JSON.stringify(sw));
      await page.screenshot({ path: `out/s2/${w}${p.replace(/[/?=]/g, "_")}.png`, fullPage: true });
    }
  }
});
