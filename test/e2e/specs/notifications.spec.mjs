import { test, expect } from "@playwright/test";

// Deleting notifications: a row's × in the bell and on /notifications, and
// Clear read. Mark all read and Clear read act on every row of the account,
// so this signs in as the second e2e account (E2E_MEDIA_USER, whose specs
// never look at notifications), not the admin inbox.spec.mjs watches.

const user = process.env.E2E_MEDIA_USER;
const password = process.env.E2E_MEDIA_PASSWORD;
test.skip(!(user && password), "set E2E_MEDIA_USER and E2E_MEDIA_PASSWORD for a second verified account");

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
  // Other specs may share this account and make notifications of their own, so
  // this one looks only at its own rows, and at counts the server returns.
  const tag = Date.now().toString(36);
  const rows = page.locator(".app-inbox-page .pb-notif", { hasText: `dismiss-${tag}-` });
  const row = (name) => page.locator(".app-inbox-page .pb-notif", { hasText: `${name} opened` });
  const count = page.locator(".pb-shell__head .pb-bell__count");
  const shown = (n) => (n ? String(n) : "");
  // Mark all read posts in the background; wait for it before clearing.
  const answer = async (path, click) => {
    const [res] = await Promise.all([page.waitForResponse((r) => r.url().endsWith(path) && r.request().method() === "POST"), click()]);
    expect(res.status()).toBe(200);
    return res.json();
  };
  const markAllRead = () => answer("/notifications/read-all", () => head.getByRole("button", { name: "Mark all read" }).click());
  const clearRead = () => answer("/notifications/clear-read", () => head.getByRole("button", { name: "Clear read" }).click());
  await expect(page.locator(".app-inbox-page .pb-note")).toHaveText("Read notifications are removed after 24 hours.");

  // Three items, each opened once by someone else: three unread rows.
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
  await expect(async () => {
    await page.reload();
    await expect(rows).toHaveCount(3, { timeout: 1000 });
  }).toPass({ timeout: 45_000 });
  await expect(row(names[0])).toHaveAttribute("data-unread", "");

  // The × deletes the row only: no read, no navigation; the count is the server's.
  let r = await answer(`/delete`, () => row(names[0]).getByRole("button", { name: `Delete notification: ${names[0]} opened` }).click());
  await expect(row(names[0])).toHaveCount(0);
  await expect(page).toHaveURL(/\/notifications$/);
  await expect(count).toHaveText(shown(r.unread));
  // Gone for good.
  await page.reload();
  await expect(rows).toHaveCount(2);

  // The bell's rows have it too, and the page's list follows.
  const bell = page.locator(".pb-shell__head .pb-bell");
  const panel = page.locator("#inbox-panel");
  await bell.click();
  await expect(panel).toBeVisible();
  const prow = panel.locator(".pb-notif", { hasText: `${names[1]} opened` });
  r = await answer(`/delete`, () => prow.getByRole("button", { name: `Delete notification: ${names[1]} opened` }).click());
  await expect(prow).toHaveCount(0);
  await expect(panel).toBeVisible();
  await expect(page).toHaveURL(/\/notifications$/);
  await expect(count).toHaveText(shown(r.unread));
  await expect(row(names[1])).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(panel).toBeHidden();

  // Clear read leaves the unread row; once read, it clears too.
  await clearRead();
  await expect(rows).toHaveCount(1);
  await expect(row(names[2])).toHaveAttribute("data-unread", "");
  await markAllRead();
  await clearRead();
  await expect(rows).toHaveCount(0);
  await page.reload();
  await expect(rows).toHaveCount(0);
  expect(errors).toEqual([]);
});
