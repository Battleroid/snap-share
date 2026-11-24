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
  await page.goto("/notifications");
  await expect(page.getByRole("heading", { level: 1, name: "Notifications" })).toBeVisible();
}

function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

test("the bell shows a visitor's first open live, and the row opens the item's stats", async ({ page, browser }) => {
  test.setTimeout(90_000);
  const errors = watchErrors(page);
  await signIn(page);
  const name = `inbox-${Date.now().toString(36)}.png`;
  const res = await page.request.put(`/api/v1/upload/${encodeURIComponent(name)}`, { data: Buffer.from(PNG, "base64"), headers: { Accept: "application/json" } });
  expect(res.status()).toBe(201);
  const item = await res.json();
  const href = item.url;

  // Open the bell and start from nothing unread.
  await page.goto("/dashboard");
  const bell = page.locator(".pb-shell__head .pb-bell");
  const count = bell.locator(".pb-bell__count");
  const panel = page.locator("#inbox-panel");
  await bell.click();
  await expect(panel).toBeVisible();
  // A second click on the bell closes the panel, a third opens it again.
  await bell.click();
  await expect(panel).toBeHidden();
  await bell.click();
  await expect(panel).toBeVisible();
  await expect(panel.locator(".pb-inbox__foot")).toContainText("All notifications");
  await panel.getByRole("button", { name: "Mark all read" }).click();
  await expect(bell).toHaveAttribute("aria-label", "Notifications");

  // Someone else opens the item. Without a reload, the open panel gets the
  // row and the bell its count (Server-Sent Events, through Caddy).
  const visitor = await browser.newContext({ userAgent: DESKTOP, ignoreHTTPSErrors: true });
  const vp = await visitor.newPage();
  expect((await vp.goto(href)).status()).toBe(200);
  await visitor.close();
  const live = panel.locator(".pb-notif", { hasText: `${name} opened` });
  await expect(live).toBeVisible({ timeout: 30_000 });
  await expect(live).toHaveAttribute("data-unread", "");
  await expect(live.locator(".pb-notif__body")).toContainText("Chrome · Linux");
  await expect(count).toHaveText(/^[1-9]\d*$/);
  await expect(bell).toHaveAttribute("aria-label", /^Notifications, \d+ unread$/);

  // Every page renders the count; the popover loads its rows when opened.
  await page.reload();
  await expect(count).toHaveText(/^[1-9]\d*$/);
  await bell.click();
  const row = panel.locator(".pb-notif", { hasText: `${name} opened` });
  await expect(row).toHaveAttribute("data-unread", "");
  await expect(row.locator(".pb-notif__time")).toHaveText(/^(now|\dm)$/);

  // Clicking the row marks it read and opens the item's stats.
  await row.click();
  await expect(page).toHaveURL(new RegExp(`/dashboard/items/${item.id}$`));
  await bell.click();
  await panel.getByRole("link", { name: "All notifications" }).click();
  await expect(page).toHaveURL(/\/notifications$/);
  const full = page.locator(".app-inbox-page .pb-notif", { hasText: `${name} opened` });
  await expect(full).toBeVisible();
  await expect(full).not.toHaveAttribute("data-unread", "");
  expect(errors).toEqual([]);
});
