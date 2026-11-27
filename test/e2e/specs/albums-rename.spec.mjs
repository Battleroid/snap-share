import { test, expect } from "@playwright/test";

// Albums of any files, rename in place, and the album grid: a zip album
// downloads its zips (the viewer takes only pictures, videos and audio),
// file tiles have Download, tiles share the row, drag handles show on hover,
// and titles and file names change in place on the item and album pages.
//
// Needs an account: E2E_USER and E2E_PASSWORD (make e2e-isolated provides one).
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
test.skip(!(user && password), "set E2E_USER and E2E_PASSWORD for an account");

const UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";
const contextOptions = { baseURL: process.env.BASE_URL, ignoreHTTPSErrors: true, userAgent: UA, viewport: { width: 1280, height: 900 }, acceptDownloads: true };

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

async function signedIn(browser, extra = {}) {
  const context = await browser.newContext({ ...contextOptions, storageState: session, ...extra });
  contexts.push(context);
  return context.newPage();
}

async function visitor(browser) {
  const context = await browser.newContext(contextOptions);
  contexts.push(context);
  return context.newPage();
}

const tag = () => `ar${Date.now().toString(36)}${Math.random().toString(36).slice(2, 5)}`;

async function upload(page, name, data) {
  const res = await page.request.put(`/api/v1/upload/${encodeURIComponent(name)}`, { data: Buffer.from(data), headers: { Accept: "application/json" } });
  expect(res.status()).toBe(201);
  return res.json();
}

async function album(page, title, ids) {
  const res = await page.request.post("/api/v1/albums", { data: { title, items: ids }, headers: { Accept: "application/json" } });
  expect(res.status()).toBe(201);
  const a = await res.json();
  return { ...a, href: new URL(a.page_url || a.url).pathname };
}

const zip = "PK\x03\x04\x14\x00\x00\x00\x00\x00";

test("an album of zips: a tile opens the zip's page, not the viewer, and Download saves the zip", async ({ browser }) => {
  const owner = await signedIn(browser);
  const t = tag();
  const a = await upload(owner, `${t}-one.zip`, zip);
  const b = await upload(owner, `${t}-two.zip`, zip);
  const al = await album(owner, `${t} files`, [a.id, b.id]);

  const page = await visitor(browser);
  await page.goto(al.href);
  const tile = page.locator("[data-member]").first();
  await tile.locator(".pb-media__frame").click();
  await expect(page).toHaveURL(new RegExp(`/${a.slug}$`));
  await expect(page.locator(".pb-lightbox[open]")).toHaveCount(0);

  await page.goto(al.href);
  const [download] = await Promise.all([page.waitForEvent("download"), tile.getByRole("link", { name: /^Download / }).click()]);
  expect(download.suggestedFilename()).toBe(`${t}-one.zip`);
});

test("album tiles share the row out, and the drag handle shows on hover and focus", async ({ browser }) => {
  const page = await signedIn(browser);
  const t = tag();
  const ids = [];
  for (let i = 0; i < 8; i++) ids.push((await upload(page, `${t}-${i}.txt`, `n${i}`)).id);
  const al = await album(page, `${t} grid`, ids);
  await page.goto(al.href);

  // The first row reaches the grid's right edge: no leftover column.
  const grid = await page.locator(".app-album-grid").boundingBox();
  const tiles = page.locator("[data-member]");
  const boxes = await Promise.all((await tiles.all()).map((x) => x.boundingBox()));
  const row = boxes.filter((x) => Math.abs(x.y - boxes[0].y) < 2);
  const right = Math.max(...row.map((x) => x.x + x.width));
  expect(row.length).toBeGreaterThan(1);
  expect(grid.x + grid.width - right).toBeLessThan(2);

  const handle = tiles.nth(1).getByRole("button", { name: /^Reorder/ });
  const opacity = () => handle.evaluate((el) => getComputedStyle(el).opacity);
  await page.mouse.move(0, 0);
  await expect.poll(opacity).toBe("0");
  await tiles.nth(1).hover();
  await expect.poll(opacity).toBe("1");
  await page.mouse.move(0, 0);
  await handle.focus();
  await expect.poll(opacity).toBe("1");
});

test("the item page renames a title and a file name in place", async ({ browser }) => {
  const page = await signedIn(browser);
  const t = tag();
  const f = await upload(page, `${t}-report.txt`, "hello");
  await page.goto(`/dashboard/items/${f.id}`);

  const name = page.locator('[data-rename-field="filename"]');
  await name.getByRole("button", { name: "Rename file name" }).click();
  const input = name.getByRole("textbox", { name: "file name" });
  await expect(input).toBeFocused();
  // The stem is selected: typing keeps the extension.
  expect(await input.evaluate((el) => [el.selectionStart, el.selectionEnd])).toEqual([0, `${t}-report`.length]);
  await page.keyboard.type(`${t}-final`);
  await page.keyboard.press("Enter");
  await page.waitForLoadState("load");
  await expect(page.locator('[data-rename-field="filename"] [data-rename-text]')).toHaveText(`${t}-final.txt`);

  const title = page.locator('[data-rename-field="title"]');
  await title.getByRole("button", { name: "Rename title" }).click();
  await title.getByRole("textbox", { name: "title" }).fill(`${t} Quarterly`);
  await title.getByRole("button", { name: "Save" }).click();
  await page.waitForLoadState("load");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(`${t} Quarterly`);

  // Escape leaves the name as it was.
  await title.getByRole("button", { name: "Rename title" }).click();
  await page.keyboard.type("nope");
  await page.keyboard.press("Escape");
  await expect(title.locator("[data-rename-text]")).toHaveText(`${t} Quarterly`);

  const got = await (await page.request.get(`/api/v1/items/${f.id}`, { headers: { Accept: "application/json" } })).json();
  expect([got.filename, got.title]).toEqual([`${t}-final.txt`, `${t} Quarterly`]);
  const dl = await page.request.get(new URL(got.download_url || `/${got.slug}/download`, process.env.BASE_URL).pathname);
  expect(dl.headers()["content-disposition"]).toContain(`filename="${t}-final.txt"`);
});

test("a blocked or empty file name stays in the field with the server's message", async ({ browser }) => {
  const page = await signedIn(browser);
  const t = tag();
  const f = await upload(page, `${t}.txt`, "x");
  await page.goto(`/dashboard/items/${f.id}`);
  const name = page.locator('[data-rename-field="filename"]');
  await name.getByRole("button", { name: "Rename file name" }).click();
  await name.getByRole("textbox").fill("   ");
  await page.keyboard.press("Enter");
  await expect(name.getByRole("alert")).toHaveText("Enter a file name.");
  await expect(name.getByRole("textbox")).toHaveAttribute("aria-invalid", "true");
});

test("a file name with markup renders as text", async ({ browser }) => {
  const page = await signedIn(browser);
  let fired = false;
  page.on("dialog", (d) => { fired = true; d.dismiss(); });
  const t = tag();
  const f = await upload(page, `${t}.txt`, "x");
  const res = await page.request.patch(`/api/v1/items/${f.id}`, { data: { filename: `<img src=x onerror=alert(1)>${t}.txt` }, headers: { Accept: "application/json" } });
  expect(res.status()).toBe(200);
  const al = await album(page, `${t} xss`, [f.id]);
  for (const href of [`/dashboard/items/${f.id}`, al.href, `/${f.slug}`]) {
    await page.goto(href);
    await expect(page.locator('img[src="x"]')).toHaveCount(0);
  }
  // Renamed again in place, the new name is text too.
  await page.goto(al.href);
  const tile = page.locator("[data-member]").first();
  await tile.getByRole("button", { name: /^Actions for/ }).click();
  await page.locator(".pb-menu[popover]:popover-open").getByRole("menuitem", { name: "Rename" }).click();
  await tile.getByRole("textbox").fill(`<svg onload=alert(2)>${t}.txt`);
  await page.keyboard.press("Enter");
  await expect(tile.locator(".pb-media__name")).toHaveText(`<svg onload=alert(2)>${t}.txt`);
  await expect(page.locator("svg[onload]")).toHaveCount(0);
  expect(fired).toBe(false);
});

test("the album page renames the album and a tile in place", async ({ browser }) => {
  const page = await signedIn(browser);
  const t = tag();
  const f = await upload(page, `${t}-a.zip`, zip);
  const g = await upload(page, `${t}-b.txt`, "b");
  const al = await album(page, `${t} trip`, [f.id, g.id]);
  await page.goto(al.href);

  const head = page.locator(".app-album-title");
  await head.getByRole("button", { name: "Rename album" }).click();
  await head.getByRole("textbox", { name: "album title" }).fill(`${t} holiday`);
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(`${t} holiday`);

  const tile = page.locator(`[data-member="${f.id}"]`);
  await tile.getByRole("button", { name: /^Actions for/ }).click();
  await page.locator(".pb-menu[popover]:popover-open").getByRole("menuitem", { name: "Rename" }).click();
  const input = tile.getByRole("textbox");
  await expect(input).toHaveValue(`${t}-a.zip`);
  expect(await input.evaluate((el) => el.selectionEnd)).toBe(`${t}-a`.length);
  await page.keyboard.type(`${t}-photos`);
  await page.keyboard.press("Enter");
  await expect(page.locator(".pb-toast", { hasText: `Renamed to ${t}-photos.zip` })).toBeVisible();
  await expect(tile.getByRole("button", { name: `Reorder ${t}-photos.zip` })).toBeAttached();
  await expect(tile.getByRole("link", { name: `Download ${t}-photos.zip` })).toBeAttached();

  await page.reload();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(`${t} holiday`);
  await expect(tile.locator(".pb-media__name")).toHaveText(`${t}-photos.zip`);
});

test("Rename in the dashboard row menu opens the drawer on the file name, stem selected", async ({ browser }) => {
  const page = await signedIn(browser);
  const t = tag();
  await upload(page, `${t}-notes.md`, "# hi");
  await page.goto(`/dashboard?q=${t}`);
  await page.getByRole("button", { name: new RegExp(`^Actions for ${t}-notes\\.md`) }).first().click();
  await page.locator(".pb-menu[popover]:popover-open").getByRole("menuitem", { name: "Rename" }).click();
  const input = page.locator("#item-edit input[name=filename]");
  await expect(input).toBeFocused();
  expect(await input.evaluate((el) => [el.selectionStart, el.selectionEnd])).toEqual([0, `${t}-notes`.length]);
});
