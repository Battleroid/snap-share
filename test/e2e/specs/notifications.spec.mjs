import { test, expect } from "@playwright/test";

// Deleting notifications: a row's × in the bell and on /notifications, and
// Clear read. Needs a verified account: E2E_USER and E2E_PASSWORD sign in
// through /login.
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
test.skip(!(user && password), "set E2E_USER and E2E_PASSWORD for a verified account");

// The stats classifier drops HeadlessChrome; look like a desktop browser.
const DESKTOP = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/159.0.0.0 Safari/537.36";
test.use({ userAgent: DESKTOP, launchOptions: { args: ["--mute-audio"] } });

// A 2x1 PNG.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4zwAE/wEHAAH/4iOeWQAAAABJRU5ErkJggg==";

test("a notification is dismissed from the page and the bell, and read ones are cleared", async ({ page, browser }) => {
  test.setTimeout(120_000);
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));

  await page.goto("/login");
  await page.locator('input[name="login"]').fill(user);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto("/notifications");
  const head = page.locator(".app-inbox-page .pb-pagehead");
  const rows = page.locator(".app-inbox-page .pb-notif");
  // Mark all read posts in the background; wait for it before clearing.
  const markAllRead = () => Promise.all([
    page.waitForResponse((r) => r.url().endsWith("/notifications/read-all")),
    head.getByRole("button", { name: "Mark all read" }).click(),
  ]);
  // Start from an empty list: everything read, then cleared.
  await markAllRead();
  await head.getByRole("button", { name: "Clear read" }).click();
  await expect(rows).toHaveCount(0);
  await expect(page.locator(".app-inbox-page .pb-inbox__empty")).toBeVisible();
  await expect(page.locator(".app-inbox-page .pb-note")).toHaveText("Read notifications are removed after 24 hours.");

  // Three items, each opened once by someone else: three unread rows.
  const tag = Date.now().toString(36);
  const names = [];
  for (const n of ["a", "b", "c"]) {
    const name = `dismiss-${tag}-${n}.png`;
    const res = await page.request.put(`/api/v1/upload/${encodeURIComponent(name)}`, { data: Buffer.from(PNG, "base64"), headers: { Accept: "application/json" } });
    expect(res.status()).toBe(201);
    const visitor = await browser.newContext({ userAgent: DESKTOP, ignoreHTTPSErrors: true });
    expect((await (await visitor.newPage()).goto((await res.json()).url)).status()).toBe(200);
    await visitor.close();
    names.push(name);
  }
  const row = (name) => rows.filter({ hasText: `${name} opened` });
  await expect(async () => {
    await page.reload();
    await expect(rows).toHaveCount(3, { timeout: 1000 });
  }).toPass({ timeout: 45_000 });
  const count = page.locator(".pb-shell__head .pb-bell__count");
  await expect(count).toHaveText("3");

  // The × deletes the row only: no read, no navigation.
  await row(names[0]).getByRole("button", { name: `Delete notification: ${names[0]} opened` }).click();
  await expect(row(names[0])).toHaveCount(0);
  await expect(page).toHaveURL(/\/notifications$/);
  await expect(count).toHaveText("2");
  await expect(page.locator("[data-unread-label]")).toHaveText("2 unread");
  // Gone for good.
  await page.reload();
  await expect(rows).toHaveCount(2);

  // The bell's rows have it too; the page's list follows.
  const bell = page.locator(".pb-shell__head .pb-bell");
  const panel = page.locator("#inbox-panel");
  await bell.click();
  await expect(panel).toBeVisible();
  const prow = panel.locator(".pb-notif", { hasText: `${names[1]} opened` });
  await prow.getByRole("button", { name: `Delete notification: ${names[1]} opened` }).click();
  await expect(prow).toHaveCount(0);
  await expect(panel).toBeVisible();
  await expect(page).toHaveURL(/\/notifications$/);
  await expect(count).toHaveText("1");
  await expect(row(names[1])).toHaveCount(0);
  await page.keyboard.press("Escape");

  // Clear read leaves the unread row; once read, it clears too.
  await head.getByRole("button", { name: "Clear read" }).click();
  await expect(rows).toHaveCount(1);
  await expect(row(names[2])).toHaveAttribute("data-unread", "");
  await markAllRead();
  await head.getByRole("button", { name: "Clear read" }).click();
  await expect(rows).toHaveCount(0);
  await expect(page.locator(".app-inbox-page .pb-inbox__empty")).toBeVisible();
  await expect(count).toHaveText("");
  await page.reload();
  await expect(rows).toHaveCount(0);
  expect(errors).toEqual([]);
});
