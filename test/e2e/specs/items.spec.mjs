import { test, expect } from "@playwright/test";

// Needs a verified account: E2E_USER and E2E_PASSWORD sign in through /login
// (the accounts track), or COOKIE="name=value" supplies a session directly.
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
const cookie = process.env.COOKIE;
test.skip(!(user && password) && !cookie, "set E2E_USER and E2E_PASSWORD (or COOKIE) for a verified account");

async function signIn(page) {
  if (cookie) {
    const [name, ...v] = cookie.split("=");
    await page.context().addCookies([{ name, value: v.join("="), url: test.info().project.use.baseURL }]);
  } else {
    await page.goto("/login");
    await page.locator('input[name="login"]').fill(user);
    await page.locator('input[name="password"]').fill(password);
    await page.getByRole("button", { name: "Sign in" }).click();
  }
  await page.goto("/");
  await expect(page.getByRole("tab", { name: "Upload" })).toBeVisible();
}

function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

// A 2x1 PNG.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4zwAE/wEHAAH/4iOeWQAAAABJRU5ErkJggg==";

test("drop zone upload shows the image on its share page", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  const dt = await page.evaluateHandle((b64) => {
    const t = new DataTransfer();
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    t.items.add(new File([bytes], "pixel.png", { type: "image/png" }));
    return t;
  }, PNG);
  const zone = page.locator("#panel-upload .pb-drop");
  await page.dispatchEvent("body", "dragenter", { dataTransfer: dt });
  await expect(zone).toHaveAttribute("data-state", "over");
  await expect(zone.locator(".pb-drop__title")).toHaveText("Release to upload 1 file");
  await expect(zone.locator(".pb-drop__icon")).toHaveClass(/pb-icon--download/);
  await page.dispatchEvent("body", "drop", { dataTransfer: dt });
  await expect(zone).not.toHaveAttribute("data-state", /.+/);
  const row = page.locator("[data-filelist] .pb-filerow").first();
  await expect(row.locator(".pb-icon--status-ok")).toBeVisible();
  const href = await row.locator(".pb-filerow__meta a").getAttribute("href");
  await page.goto(href);
  const img = page.locator(".pb-media__frame--contain img");
  await expect(img).toBeVisible();
  expect(await img.evaluate((i) => i.naturalWidth)).toBe(2);
  await expect(page.getByRole("button", { name: "Copy link" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("a rejected drop explains why", async ({ page }) => {
  await signIn(page);
  await page.locator("#panel-upload .pb-drop").evaluate((z) => { z.dataset.max = "1"; });
  const dt = await page.evaluateHandle(() => {
    const t = new DataTransfer();
    t.items.add(new File([new Uint8Array(2)], "big.bin"));
    return t;
  });
  await page.dispatchEvent("body", "dragenter", { dataTransfer: dt });
  await page.dispatchEvent("body", "drop", { dataTransfer: dt });
  const zone = page.locator("#panel-upload .pb-drop");
  await expect(zone).toHaveAttribute("data-state", "error");
  await expect(zone.locator(".pb-drop__hint")).toContainText("over the");
});

test("paste opens a highlighted page", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  await page.getByRole("tab", { name: "Paste" }).click();
  const form = page.locator("[data-paste-form]");
  await form.locator('input[name="name"]').fill("main.go");
  await form.locator('textarea[name="content"]').fill("package main\n\nfunc main() {}\n");
  await page.getByRole("button", { name: "Create paste" }).click();
  const link = form.locator("[data-result] a");
  await expect(link).toBeVisible();
  await page.goto(await link.getAttribute("href"));
  await expect(page.locator(".pb-code__hl .tok-k").first()).toHaveText("package");
  await expect(page.locator(".pb-code__lang")).toHaveText("go");
  expect(errors).toEqual([]);
});

test("short link redirects to its target", async ({ page }) => {
  await signIn(page);
  await page.getByRole("tab", { name: "Link" }).click();
  const form = page.locator("[data-link-form]");
  await form.locator('input[name="url"]').fill("https://example.com/landing?x=1");
  await page.getByRole("button", { name: "Shorten" }).click();
  const link = form.locator("[data-result] a");
  await expect(link).toBeVisible();
  const res = await page.request.get(await link.getAttribute("href"), { maxRedirects: 0 });
  expect(res.status()).toBe(302);
  expect(res.headers()["location"]).toBe("https://example.com/landing?x=1");
});

test("a file dropped while the Secret tab is open is never uploaded", async ({ page }) => {
  await signIn(page);
  await page.getByRole("tab", { name: "Secret" }).click();
  const before = await (await page.request.get("/api/v1/items")).json();
  const dt = await page.evaluateHandle((b64) => {
    const t = new DataTransfer();
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    t.items.add(new File([bytes], "private.png", { type: "image/png" }));
    return t;
  }, PNG);
  await page.dispatchEvent("#panel-secret .pb-drop", "dragenter", { dataTransfer: dt });
  await page.dispatchEvent("#panel-secret .pb-drop", "drop", { dataTransfer: dt });
  // Still on the Secret tab, and the upload list stayed empty.
  await expect(page.getByRole("tab", { name: "Secret" })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#panel-upload [data-filelist] .pb-filerow")).toHaveCount(0);
  await page.waitForTimeout(500);
  const after = await (await page.request.get("/api/v1/items")).json();
  // Compare ids only: media jobs from other specs may update items meanwhile.
  const ids = (l) => l.items.map((i) => i.id);
  expect(ids(after)).toEqual(ids(before));
});
