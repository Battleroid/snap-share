import { test, expect } from "@playwright/test";

// Friction fixes from the persona walkthroughs: the generated secret
// passphrase on the result card, least-privilege API key defaults, and the
// audit table at laptop widths.
//
// Needs an admin account: E2E_USER and E2E_PASSWORD (make e2e-isolated provides one).
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
test.skip(!(user && password), "set E2E_USER and E2E_PASSWORD for an admin account");

const UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";
const contextOptions = { baseURL: process.env.BASE_URL, ignoreHTTPSErrors: true, userAgent: UA, viewport: { width: 1280, height: 900 } };

// One sign-in for the file, shared by every test (sign-ins per account are rate limited).
test.describe.configure({ mode: "serial" });
let session;
const contexts = [];

test.beforeAll(async ({ browser }) => {
  const context = await browser.newContext(contextOptions);
  const page = await context.newPage();
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(user);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Upload" })).toBeVisible();
  session = await context.storageState();
  await context.close();
});

test.afterEach(async () => {
  for (const c of contexts.splice(0)) await c.close();
});

async function signedIn(browser, viewport) {
  const context = await browser.newContext({ ...contextOptions, storageState: session, ...(viewport ? { viewport } : {}) });
  contexts.push(context);
  return context.newPage();
}

test("a generated secret passphrase is shown with Copy on the result card, and is never sent", async ({ browser }) => {
  const page = await signedIn(browser);
  const sent = [];
  page.on("request", (r) => sent.push(`${r.url()} ${r.postData() ?? ""}`));
  await page.goto("/new/secret");
  // No email or ntfy channel is on for this account: the form says where to turn one on.
  const hint = page.locator("[data-notify-hint]");
  if (await hint.count()) await expect(hint.getByRole("link")).toHaveAttribute("href", "/settings/notifications");
  else await expect(page.getByLabel("Notify me when it is opened")).toBeVisible();

  await page.locator("#sc-text").fill("the eagle lands at noon");
  await page.getByRole("button", { name: "Generate passphrase" }).click();
  const pass = await page.locator("#sc-pass").inputValue();
  expect(pass.length).toBeGreaterThan(10);
  await page.getByRole("button", { name: "Create secret" }).click();

  const out = page.locator("#secret-pass-out");
  await expect(out).toBeVisible();
  await expect(out).toHaveValue(pass);
  await expect(page.locator("[data-result-passgen]").getByRole("button", { name: "Copy" })).toBeVisible();
  expect(sent.some((s) => s.includes(pass) || s.includes(encodeURIComponent(pass)))).toBe(false);

  // Another secret with a passphrase typed by hand: only the reminder shows, never the typed value.
  await page.getByRole("button", { name: "Create another" }).click();
  await expect(out).toBeHidden();
  await page.locator("#sc-text").fill("second");
  await page.locator("#sc-pass").fill("typed by hand");
  await page.getByRole("button", { name: "Create secret" }).click();
  await expect(page.locator("#secret-link")).toBeVisible();
  await expect(page.locator("[data-result-passgen]")).toBeHidden();
  await expect(page.locator("[data-result-pass]")).toBeVisible();
  await expect(out).toHaveValue("");
});

test("a new API key is upload-only by default, with Upload only and Full access presets", async ({ browser }) => {
  const page = await signedIn(browser);
  await page.goto("/settings/keys/new");
  const box = (name) => page.locator(`input[name="scope"][value="${name}"]`);
  await expect(box("items:write")).toBeChecked();
  await expect(box("items:read")).not.toBeChecked();
  await expect(box("items:delete")).not.toBeChecked();
  await expect(page.getByRole("radio", { name: "Upload only" })).toBeChecked();

  await page.getByText("Full access", { exact: true }).click();
  for (const s of ["items:read", "items:write", "items:delete"]) await expect(box(s)).toBeChecked();
  await expect(page.getByRole("radio", { name: "Full access" })).toBeChecked();

  // Ticking by hand leaves both presets unselected.
  await box("items:delete").uncheck();
  await expect(page.getByRole("radio", { name: "Full access" })).not.toBeChecked();
  await expect(page.getByRole("radio", { name: "Upload only" })).not.toBeChecked();

  await page.getByText("Upload only", { exact: true }).click();
  await expect(box("items:read")).not.toBeChecked();
  await expect(box("items:write")).toBeChecked();
});

test("audit: no sideways scroll at 1120 and 1280, and the filters apply on change", async ({ browser }) => {
  const page = await signedIn(browser, { width: 1120, height: 900 });
  await page.goto("/admin/audit");
  await expect(page.locator(".app-audit .pb-table tbody tr").first()).toBeVisible();
  for (const width of [1120, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    expect(await page.evaluate(() => [...document.querySelectorAll(".pb-tablescroll")].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);
    // The details column is the one that matters; IP and target drop out of the row.
    await expect(page.locator('.app-audit th[data-k="details"]')).toBeVisible();
    await expect(page.locator('.app-audit th[data-k="ip"]')).toBeHidden();
  }

  // Choosing an action filters at once; the Filter button is for use without script.
  await expect(page.getByRole("button", { name: "Filter" })).toBeHidden();
  const action = await page.locator('select[name="action"] option').evaluateAll((os) => os.map((o) => o.value).filter(Boolean).at(-1));
  await page.locator('select[name="action"]').selectOption(action);
  await expect(page).toHaveURL(new RegExp(`action=${encodeURIComponent(action).replace(/\./g, "\\.")}`));
});

test("a ShareX download says which key it made", async ({ browser }) => {
  const page = await signedIn(browser);
  await page.goto("/settings/integrations");
  const [download] = await Promise.all([page.waitForEvent("download"), page.locator("form[data-sharex] button").first().click()]);
  expect(download.suggestedFilename()).toMatch(/\.sxcu$/);
  await expect(page.locator(".pb-toast", { hasText: /^Created key ShareX uploader \d{4}-\d\d-\d\d$/ })).toBeVisible();
});

test("album tiles: Space on the handle, arrows and Space reorder them, and the tile menu opens under its button", async ({ browser }) => {
  const page = await signedIn(browser);
  const tag = `al${Date.now().toString(36)}`;
  const ids = [];
  for (const n of ["one", "two", "three"]) {
    const res = await page.request.put(`/api/v1/upload/${tag}-${n}.txt`, { data: Buffer.from(n), headers: { Accept: "application/json" } });
    expect(res.status()).toBe(201);
    ids.push((await res.json()).id);
  }
  const made = await page.request.post("/api/v1/albums", { data: { title: `${tag} album`, items: ids }, headers: { Accept: "application/json" } });
  expect(made.status()).toBe(201);
  const album = await made.json();
  await page.goto(album.page_url || album.url);
  const names = () => page.locator("[data-member] .pb-media__name").allTextContents();
  const before = await names();
  expect(before).toHaveLength(3);

  const handle = page.locator("[data-member]").first().getByRole("button", { name: /^Reorder/ });
  await handle.focus();
  await page.keyboard.press("Space");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Space");
  await expect(page.locator(".pb-toast", { hasText: "Order saved" })).toBeVisible();
  expect(await names()).toEqual([before[1], before[0], before[2]]);
  await page.reload();
  expect(await names()).toEqual([before[1], before[0], before[2]]); // the server kept it

  // The tile's menu opens under its own button, not at the page's corner.
  const button = page.locator("[data-member]").nth(1).getByRole("button", { name: /^Actions for/ });
  await button.click();
  const menu = page.locator(".pb-menu[popover]:popover-open");
  await expect(menu).toBeVisible();
  const [b, m] = [await button.boundingBox(), await menu.boundingBox()];
  expect(m.y).toBeGreaterThanOrEqual(b.y + b.height - 2);
  expect(m.y).toBeLessThan(b.y + b.height + 40);
  expect(Math.abs(m.x - b.x)).toBeLessThan(260);
});
