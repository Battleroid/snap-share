// One-time secrets in the browser: create, reveal in a fresh context,
// tombstone on the second open, passphrase flow, file download.
//
// Creating needs a signed-in user with a verified email. Set E2E_USER and
// E2E_PASSWORD for an account on the stack (signs in through /login), or
// E2E_COOKIE=name=value to use a session cookie directly (fake-auth test
// harnesses). Without either, the tests are skipped.
import { test, expect } from "@playwright/test";

// The stats classifier drops HeadlessChrome; look like a normal browser.
const UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";
test.use({ userAgent: UA });

const LINK = /\/s\/[A-Za-z0-9_-]{22}#[A-Za-z0-9_-]{43}$/;

async function signIn(context, page, baseURL) {
  if (process.env.E2E_COOKIE) {
    const [name, ...rest] = process.env.E2E_COOKIE.split("=");
    await context.addCookies([{ name, value: rest.join("="), url: baseURL }]);
    return;
  }
  const user = process.env.E2E_USER;
  const pass = process.env.E2E_PASSWORD;
  test.skip(!user || !pass, "set E2E_USER and E2E_PASSWORD (a verified account) or E2E_COOKIE");
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(user);
  await page.locator('input[name="password"]').fill(pass);
  await page.locator('form button[type="submit"]').click();
  await expect(page).not.toHaveURL(/\/login/);
}

function watchErrors(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => {
    // A refused claim (403) or a missing secret (404) is logged by the
    // browser as a failed resource; that is expected here.
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(m.text());
  });
  return errors;
}

async function createText(page, text, { passphrase = "", views } = {}) {
  await page.goto("/new/secret");
  await page.locator('textarea[name="text"]').fill(text);
  if (views) await page.locator('select[name="views"]').selectOption(String(views));
  if (passphrase) await page.locator('input[name="passphrase"]').fill(passphrase);
  await page.getByRole("button", { name: "Create secret" }).click();
  const link = page.locator("#secret-link");
  await expect(link).toHaveValue(LINK, { timeout: 20_000 });
  await expect(page.getByText("Copy it now; it can't be shown again.")).toBeVisible();
  return link.inputValue();
}

async function recipient(browser) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, userAgent: UA });
  const page = await ctx.newPage();
  return { ctx, page, errors: watchErrors(page) };
}

test("text secret: revealed once, then a tombstone", async ({ browser, context, page, baseURL }) => {
  const errors = watchErrors(page);
  await signIn(context, page, baseURL);
  const url = await createText(page, "the eagle lands at noon");
  expect(errors).toEqual([]);

  const a = await recipient(browser);
  await a.page.goto(url);
  // The key leaves the address bar at once.
  await expect(a.page).toHaveURL(/\/s\/[A-Za-z0-9_-]{22}$/);
  await expect(a.page.getByText("1 view left")).toBeVisible();
  await a.page.getByRole("button", { name: "Reveal" }).click();
  await expect(a.page.locator("#secret-text")).toHaveText("the eagle lands at noon");
  await expect(a.page.getByText("That was the last view")).toBeVisible();
  expect(a.errors).toEqual([]);
  await a.ctx.close();

  const b = await recipient(browser);
  await b.page.goto(url);
  await expect(b.page.getByText(/^Opened \d{4}-\d\d-\d\d \d\d:\d\d$/)).toBeVisible();
  await expect(b.page.getByRole("button", { name: "Reveal" })).toBeHidden();
  await b.ctx.close();

  // A link without its key says so, and asks the server nothing.
  const c = await recipient(browser);
  await c.page.goto(url.split("#")[0]);
  await expect(c.page.getByText("The link is incomplete")).toBeVisible();
  await c.ctx.close();
});

test("passphrase flow", async ({ browser, context, page, baseURL }) => {
  await signIn(context, page, baseURL);
  const url = await createText(page, "vault code 4471", { passphrase: "correct horse battery staple", views: 2 });
  await expect(page.locator("[data-result-pass]")).toBeVisible();

  const r = await recipient(browser);
  await r.page.goto(url);
  await expect(r.page.getByText("2 views left")).toBeVisible();
  const field = r.page.locator('input[name="passphrase"]');
  await expect(field).toBeVisible();
  await field.fill("wrong horse");
  await r.page.getByRole("button", { name: "Reveal" }).click();
  await expect(r.page.getByText("Wrong passphrase. 9 attempts left.")).toBeVisible({ timeout: 20_000 });
  await field.fill("correct horse battery staple");
  await r.page.getByRole("button", { name: "Reveal" }).click();
  await expect(r.page.locator("#secret-text")).toHaveText("vault code 4471", { timeout: 20_000 });
  await expect(r.page.getByText("1 view left for others.")).toBeVisible();
  expect(r.errors).toEqual([]);
  await r.ctx.close();
});

test("file secret: decrypted download and image preview", async ({ browser, context, page, baseURL }) => {
  await signIn(context, page, baseURL);
  await page.goto("/new/secret");
  await page.locator('.pb-seg label:has(input[value="file"])').click();
  // An 8x8 PNG.
  const png = Buffer.from(
    "89504e470d0a1a0a0000000d49484452000000080000000808020000004b6d29dc0000001149444154789c63b8e0a080" +
      "15310c2d0900de7b4c01943a40ca0000000049454e44ae426082",
    "hex",
  );
  await page.locator('input[type="file"]').setInputFiles({ name: "dot.png", mimeType: "image/png", buffer: png });
  await expect(page.locator("[data-file-name]")).toHaveText("dot.png");
  await page.getByRole("button", { name: "Create secret" }).click();
  await expect(page.locator("#secret-link")).toHaveValue(LINK, { timeout: 20_000 });
  const url = await page.locator("#secret-link").inputValue();

  const r = await recipient(browser);
  await r.page.goto(url);
  await r.page.getByRole("button", { name: "Reveal" }).click();
  const dl = r.page.locator("[data-download]");
  await expect(dl).toBeVisible({ timeout: 20_000 });
  await expect(dl).toHaveAttribute("download", "dot.png");
  await expect(dl).toHaveAttribute("href", /^blob:/);
  await expect(r.page.locator("[data-preview] img")).toBeVisible();
  const [download] = await Promise.all([r.page.waitForEvent("download"), dl.click()]);
  expect(download.suggestedFilename()).toBe("dot.png");
  const path = await download.path();
  const { readFile } = await import("node:fs/promises");
  expect(Buffer.compare(await readFile(path), png)).toBe(0);
  expect(r.errors).toEqual([]);
  await r.ctx.close();
});
