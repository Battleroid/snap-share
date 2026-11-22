// Regression tests for the security review of one-time secrets in the
// browser: the review's proofs of concept, inverted (each fails if its
// weakness comes back), plus the paste box at /s and the fragment handling
// of the token pages.
import { test, expect } from "@playwright/test";

const UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";
test.use({ userAgent: UA });

const LINK = /\/s\/[A-Za-z0-9_-]{22}#[A-Za-z0-9_-]{43}$/;
const NO_KEY = /\/s\/[A-Za-z0-9_-]{22}$/;

async function signIn(page) {
  const user = process.env.E2E_USER;
  const pass = process.env.E2E_PASSWORD;
  test.skip(!user || !pass, "set E2E_USER and E2E_PASSWORD");
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(user);
  await page.locator('input[name="password"]').fill(pass);
  await page.locator('form button[type="submit"]').click();
  await expect(page).not.toHaveURL(/\/login/);
}

async function createText(page, text) {
  await page.goto("/new/secret");
  await page.locator('textarea[name="text"]').fill(text);
  await page.getByRole("button", { name: "Create secret" }).click();
  const link = page.locator("#secret-link");
  await expect(link).toHaveValue(LINK, { timeout: 20_000 });
  return link.inputValue();
}

// RT-FORM1: a module that fails to load must not turn "Create secret" into a
// native GET with the plaintext and passphrase in the URL.
test("composer: a failed module load sends nothing", async ({ page }) => {
  await signIn(page);
  const sent = [];
  page.on("request", (r) => sent.push(r.url()));
  await page.route(/\/js\/eff-wordlist/, (r) => r.abort());
  await page.goto("/new/secret");
  const before = page.url();
  await page.locator('textarea[name="text"]').fill("RT-PLAINTEXT launch code 0000");
  await page.locator('input[name="passphrase"]').fill("RT-PASSPHRASE correct horse");
  const button = page.getByRole("button", { name: "Create secret" });
  await expect(button).toBeDisabled();
  await button.click({ force: true });
  await page.locator('input[name="passphrase"]').press("Enter");
  await page.waitForTimeout(1000);
  expect(page.url()).toBe(before);
  expect(sent.some((u) => u.includes("RT-PLAINTEXT") || u.includes("RT-PASSPHRASE"))).toBe(false);
});

// RT-FORM2: Enter before secret-new.js has run sends nothing either.
test("composer: Enter before secret-new.js has run sends nothing", async ({ page }) => {
  await signIn(page);
  const sent = [];
  page.on("request", (r) => sent.push(r.url()));
  await page.route(/\/js\/secret-new/, async (r) => {
    await new Promise((res) => setTimeout(res, 3000));
    await r.continue();
  });
  await page.goto("/new/secret", { waitUntil: "commit" });
  await page.locator('textarea[name="text"]').fill("RT-SLOW plaintext");
  await page.locator('input[name="passphrase"]').fill("RT-SLOW-PASS");
  await page.locator('input[name="passphrase"]').press("Enter");
  await page.waitForTimeout(1000);
  expect(page.url()).not.toContain("RT-SLOW");
  expect(sent.some((u) => u.includes("RT-SLOW"))).toBe(false);
  // Once the module has run, the button works.
  await expect(page.getByRole("button", { name: "Create secret" })).toBeEnabled({ timeout: 10_000 });
});

// RT-FRAG: the key leaves the address bar even when the module graph fails
// to load.
test("reveal page: key leaves the address bar when e2e.js fails to load", async ({ browser, page }) => {
  await signIn(page);
  const url = await createText(page, "rt fragment test");
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, userAgent: UA });
  const r = await ctx.newPage();
  await r.route(/\/js\/e2e\.js/, (route) => route.abort());
  await r.goto(url);
  await expect(r).toHaveURL(NO_KEY);
  // Nothing can be revealed without the module; the button stays disabled.
  await expect(r.locator("[data-reveal-btn]")).toBeDisabled();
  await ctx.close();
});

// Exploratory (no assertion on the outcome): is the revealed plaintext
// restored from the back/forward cache after navigating away?
test("reveal page: plaintext after back navigation (exploratory)", async ({ browser, page }) => {
  await signIn(page);
  const url = await createText(page, "rt bfcache plaintext");
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, userAgent: UA });
  const r = await ctx.newPage();
  await r.goto(url);
  await r.getByRole("button", { name: "Reveal" }).click();
  await expect(r.locator("#secret-text")).toHaveText("rt bfcache plaintext");
  await r.goto("/login");
  await r.goBack();
  await r.waitForTimeout(1000);
  const shown = await r.locator("#secret-text").textContent().catch(() => null);
  console.log("after back navigation, #secret-text =", JSON.stringify(shown));
  await ctx.close();
});

// The paste box at /s opens a link in place: the address bar (and so the
// history entry) never holds the key.
test("paste box: opens a link without navigating", async ({ browser, page, baseURL }) => {
  await signIn(page);
  const url = await createText(page, "pasted, not navigated");
  await expect(page.getByText("Recipients can paste the link at")).toBeVisible();

  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, userAgent: UA });
  const r = await ctx.newPage();
  const navigations = [];
  r.on("framenavigated", (f) => {
    if (f === r.mainFrame()) navigations.push(f.url());
  });
  await r.goto("/s");
  const field = r.locator('input[name="link"]');
  await expect(field).toBeVisible();
  const entries = await r.evaluate(() => history.length);
  // Another server's link is refused before anything is sent.
  await field.fill(url.replace(new URL(baseURL).host, "other.example"));
  await r.getByRole("button", { name: "Open" }).click();
  await expect(r.getByText("This link is for other.example. Open it there.")).toBeVisible();

  await field.fill(url);
  await r.getByRole("button", { name: "Open" }).click();
  await expect(r.getByText("1 view left")).toBeVisible();
  await r.getByRole("button", { name: "Reveal" }).click();
  await expect(r.locator("#secret-text")).toHaveText("pasted, not navigated");
  expect(r.url()).toBe(new URL("/s", baseURL).href);
  expect(navigations.every((u) => !u.includes("#"))).toBe(true);
  expect(await r.evaluate(() => history.length)).toBe(entries); // no new history entry
  await ctx.close();
});

// Token pages strip the fragment before their module runs (or fails to).
test("token pages: the fragment leaves the address bar without the module", async ({ page }) => {
  await page.route(/\/js\/token-(page|x)\.js/, (r) => r.abort());
  for (const path of ["/verify#t=RT-TOKEN", "/reset#t=RT-TOKEN", "/invite#t=RT-TOKEN", "/x#RT-TOKEN"]) {
    await page.goto(path);
    expect(page.url()).not.toContain("RT-TOKEN");
  }
});
