import { test, expect } from "@playwright/test";

// Needs a verified account: E2E_USER and E2E_PASSWORD sign in through /login,
// or COOKIE="name=value" supplies a session directly.
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
const cookie = process.env.COOKIE;
test.skip(!(user && password) && !cookie, "set E2E_USER and E2E_PASSWORD (or COOKIE) for a verified account");

// The stats classifier drops HeadlessChrome; look like a desktop browser.
const DESKTOP = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/159.0.0.0 Safari/537.36";
test.use({ userAgent: DESKTOP });

// A 2x1 PNG.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4zwAE/wEHAAH/4iOeWQAAAABJRU5ErkJggg==";

// The sign-in helper lands on the stats page: it exists for every account, and
// the specs do not depend on how the composer or the dashboard look.
async function signIn(page) {
  if (cookie) {
    const [name, ...v] = cookie.split("=");
    await page.context().addCookies([{ name, value: v.join("="), url: test.info().project.use.baseURL }]);
  } else {
    await page.goto("/login");
    await page.locator('input[name="login"]').fill(user);
    await page.locator('input[name="password"]').fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
  }
  await page.goto("/dashboard/stats");
  await expect(page.getByRole("heading", { level: 1, name: "Stats" })).toBeVisible();
}

// upload puts a file through the API and returns the new item.
async function upload(page, name) {
  const res = await page.request.put(`/api/v1/upload/${encodeURIComponent(name)}`, {
    data: Buffer.from(PNG, "base64"), headers: { Accept: "application/json" },
  });
  expect(res.status()).toBe(201);
  const item = await res.json();
  return { id: item.id, href: item.url };
}

function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

test("the owner's stats page shows a visitor's view, and Your stats adds it up", async ({ page, browser }) => {
  test.setTimeout(120_000);
  const errors = watchErrors(page);
  await signIn(page);
  const name = `stats-${Date.now().toString(36)}.png`;
  const item = await upload(page, name);
  const href = item.href;

  const stats = `/dashboard/items/${item.id}/stats`;
  const badge = page.locator(".pb-pagehead .pb-badge").first();
  const tile = (label) => page.locator(".pb-tile").filter({ has: page.locator(".pb-tile__label", { hasText: new RegExp(`^${label} · 30d$`) }) }).locator(".pb-tile__value");
  await page.goto(stats);
  await expect(page.getByRole("heading", { level: 1 })).toContainText(name);
  await expect(badge).toHaveText("not opened");
  // Back to Your stats through the breadcrumb.
  await expect(page.getByRole("navigation", { name: "Breadcrumb" }).getByRole("link", { name: "Stats" })).toHaveAttribute("href", "/dashboard/stats");
  await expect(tile("views")).toHaveText("0");
  await expect(page.getByText("Your own visits are not counted.")).toBeVisible();
  await expect(page.locator(".pb-chart svg .pb-grid").first()).toBeAttached();

  // The owner's own visit is not counted; a second browser's is.
  await page.goto(href);
  const visitor = await browser.newContext({ userAgent: DESKTOP, ignoreHTTPSErrors: true });
  const vp = await visitor.newPage();
  const res = await vp.goto(href);
  expect(res.status()).toBe(200);
  await visitor.close();

  // Events are written every few seconds; reload until the view shows.
  await expect.poll(async () => {
    await page.goto(stats);
    return tile("views").textContent();
  }, { timeout: 45_000, intervals: [2_000] }).toBe("1");
  await expect.poll(async () => {
    await page.goto(stats);
    return badge.textContent();
  }, { timeout: 30_000, intervals: [2_000] }).toBe("opened");
  await expect(tile("visitors")).toHaveText("1");
  const events = page.locator("[data-stats-events] tbody tr");
  await expect(events).toHaveCount(1);
  await expect(events.first()).toContainText("view");
  await expect(events.first()).toContainText("Chrome · Linux");
  await expect(page.locator(".app-stats-tops")).toContainText("Chrome · Linux");

  // The range control reloads the page with another window.
  await page.locator('.pb-seg input[value="7"]').check({ force: true });
  await expect(page).toHaveURL(/days=7/);
  await expect(page.locator(".pb-tile__label").first()).toHaveText("views · 7d");

  // CSV export of the raw events.
  const csv = await page.request.get(`/dashboard/items/${item.id}/events.csv`);
  expect(csv.headers()["content-disposition"]).toMatch(/^attachment; filename=".+-events\.csv"$/);
  const text = await csv.text();
  expect(text.split("\n")[0]).toBe("time,kind,ip,user_agent,client,bot,referrer");
  expect(text).toContain("Chrome · Linux");

  // Your stats: the same view, summed over every item, with this item on top.
  await page.goto("/dashboard/stats");
  await expect(page.getByRole("link", { name: "Stats", exact: true }).first()).toHaveAttribute("aria-current", "page");
  await expect(page.locator(".pb-tile__label").first()).toHaveText("views · 30d");
  await expect(page.locator(".pb-tile__value").first()).not.toHaveText("0");
  await expect(page.locator(".pb-chart svg .pb-grid").first()).toBeAttached();
  const top = page.getByRole("link", { name }).first();
  await expect(top).toHaveAttribute("href", stats);
  await expect(page.getByRole("heading", { name: "Storage" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Items by type" })).toBeVisible();
  await expect(page.locator(".app-typerow", { hasText: "image" })).toBeVisible();
  // The storage bar's segments get their widths from data-vars (no inline styles).
  expect(await page.locator(".pb-storage__bar > span").first().evaluate((el) => el.style.getPropertyValue("--w"))).toMatch(/^[\d.]+%$/);
  await page.locator(".pb-seg input[value=\"90\"]").check({ force: true });
  await expect(page).toHaveURL(/days=90/);
  await expect(page.locator(".pb-tile__label").first()).toHaveText("views · 90d");
  await page.goto("/dashboard/stats");
  await top.click();
  await expect(page).toHaveURL(new RegExp(`${stats}$`));

  // A phone: nothing scrolls sideways on either page.
  await page.setViewportSize({ width: 390, height: 844 });
  for (const path of ["/dashboard/stats", stats]) {
    await page.goto(path);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
  expect(errors).toEqual([]);
});

test("notification switches apply at once", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  await page.goto("/settings/notifications");
  await expect(page.locator('a[href="/settings/notifications"]')).toHaveAttribute("aria-current", "page");
  // A server that does not resolve, so nothing is ever delivered from this run.
  await page.locator('input[name="server"]').fill("https://ntfy.invalid");
  await page.locator('input[name="topic"]').fill("snap-e2e");
  await page.locator('input[name="token"]').fill("tk_e2etoken");
  await page.locator("form[action='/settings/notifications/ntfy']").getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("ntfy settings saved.")).toBeVisible();
  await expect(page.getByText("access token · set")).toBeVisible();
  expect(await page.content()).not.toContain("tk_e2etoken");

  const sw = page.getByRole("switch", { name: /ntfy/ });
  await expect(sw).toBeEnabled();
  await expect(sw).not.toBeChecked();
  const saved = page.waitForResponse((r) => r.url().endsWith("/settings/notifications/channel") && r.status() === 200);
  await sw.check({ force: true });
  await saved;
  await page.reload();
  await expect(sw).toBeChecked();
  const off = page.waitForResponse((r) => r.url().endsWith("/settings/notifications/channel") && r.status() === 200);
  await sw.uncheck({ force: true });
  await off;
  await page.reload();
  await expect(sw).not.toBeChecked();
  expect(errors).toEqual([]);
});
