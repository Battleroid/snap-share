import { test, expect } from "@playwright/test";

// Needs a verified account: E2E_USER and E2E_PASSWORD (make e2e-isolated provides an admin).
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
test.skip(!(user && password), "set E2E_USER and E2E_PASSWORD for a verified account");

// A desktop browser: headless user agents are not counted as people.
test.use({
  userAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
});

async function signIn(page) {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(user);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("tab", { name: "Upload" })).toBeVisible();
}

function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

// A 2x1 PNG.
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4zwAE/wEHAAH/4iOeWQAAAABJRU5ErkJggg==", "base64");

async function upload(page, name, data) {
  const res = await page.request.put(`/api/v1/upload/${encodeURIComponent(name)}`, { data, headers: { Accept: "application/json" } });
  expect(res.status()).toBe(201);
  return res.json();
}

test("dashboard: search, type filter, bulk expiry, album from the selection, lightbox", async ({ page }) => {
  test.setTimeout(60_000);
  const errors = watchErrors(page);
  await signIn(page);
  const tag = `k1${Date.now().toString(36)}`;
  await upload(page, `${tag}-cat.png`, PNG);
  await upload(page, `${tag}-dog.png`, PNG);
  await upload(page, `${tag}-notes.txt`, Buffer.from("plain notes"));

  await page.goto("/dashboard");
  const rows = page.locator("#dash-results tbody tr[data-row]");
  const search = page.getByRole("searchbox", { name: "Search" });

  // Search narrows the table in place and keeps the state in the URL.
  await search.fill(tag);
  await expect(rows).toHaveCount(3);
  await expect(page).toHaveURL(new RegExp(`[?&]q=${tag}`));
  await search.fill(`${tag}-cat`);
  await expect(rows).toHaveCount(1);
  await search.fill(tag);
  await expect(rows).toHaveCount(3);

  // Type pills filter and show counts.
  const imagePill = page.locator('#dash-results [data-type="image"]');
  await expect(imagePill.locator(".pb-pill__count")).toHaveText("2");
  await imagePill.click();
  await expect(rows).toHaveCount(2);
  await expect(page).toHaveURL(/type=image/);
  await expect(page.locator('#dash-results [data-type="image"]')).toHaveAttribute("aria-pressed", "true");

  // Selection: row checkboxes and the tri-state header.
  const count = page.locator("[data-selcount]");
  await rows.nth(0).locator("[data-check]").check();
  await expect(count).toHaveText("1 selected");
  await expect(page.locator("[data-check-all]")).toHaveJSProperty("indeterminate", true);
  await rows.nth(1).locator("[data-check]").check();
  await expect(count).toHaveText("2 selected");
  await expect(page.locator("[data-check-all]")).toBeChecked();

  // Bulk expiry.
  const actions = page.getByRole("button", { name: "Actions", exact: true });
  await actions.click();
  await page.getByRole("menuitem", { name: "Set expiry" }).click();
  const expiry = page.locator("#dash-expiry");
  await expect(expiry).toBeVisible();
  await expect(expiry).toContainText("For 2 items.");
  await expiry.locator('select[name="expires"]').selectOption("7d");
  await expiry.getByRole("button", { name: "Set expiry" }).click();
  await expect(expiry).toBeHidden();
  await expect(page.locator("[data-dash-notice]")).toContainText("Expiry set on 2 items.");
  for (const i of [0, 1]) await expect(rows.nth(i).locator('[data-col="expires"]')).toHaveText(/^\d{4}-\d\d-\d\d \d\d:\d\d$/);

  // A new album from the selection.
  await page.locator("[data-check-all]").check();
  await expect(count).toHaveText("2 selected");
  await actions.click();
  await page.getByRole("menuitem", { name: "Add to album" }).click();
  const albumDialog = page.locator("#dash-album");
  await albumDialog.locator('select[name="album_id"]').selectOption("new");
  await albumDialog.locator('input[name="title"]').fill(`${tag} album`);
  await albumDialog.getByRole("button", { name: "Add to album" }).click();
  const open = page.locator("[data-dash-notice] a", { hasText: "Open the album" });
  await expect(open).toBeVisible();
  await expect(count).toHaveText("none selected"); // the table has refreshed

  // The album page and its lightbox.
  await page.goto(await open.getAttribute("href"));
  await expect(page.getByRole("heading", { name: `${tag} album` })).toBeVisible();
  const cards = page.locator("[data-lightbox]");
  await expect(cards).toHaveCount(2);
  await cards.first().click();
  const box = page.locator("#lightbox");
  await expect(box).toBeVisible();
  await expect(box.locator("[data-lightbox-pos]")).toHaveText("1 of 2");
  const img = box.locator("[data-lightbox-stage] img");
  await expect(img).toBeVisible();
  await expect.poll(() => img.evaluate((i) => i.naturalWidth)).toBe(2);
  await page.keyboard.press("ArrowRight");
  await expect(box.locator("[data-lightbox-pos]")).toHaveText("2 of 2");
  await page.keyboard.press("ArrowRight");
  await expect(box.locator("[data-lightbox-pos]")).toHaveText("1 of 2");
  await page.keyboard.press("Escape");
  await expect(box).toBeHidden();
  await expect(cards.first()).toBeFocused();
  expect(errors).toEqual([]);
});

test("grid view, switching views, and delete after the confirm", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  const tag = `k1g${Date.now().toString(36)}`;
  for (const n of ["one", "two", "three"]) await upload(page, `${tag}-${n}.png`, PNG);
  await page.goto(`/dashboard?q=${tag}&view=grid`);
  const cards = page.locator("#dash-results .app-dash-grid > li");
  await expect(cards).toHaveCount(3);
  await expect(cards.first().locator("img")).toBeVisible();
  await page.getByRole("radio", { name: "table" }).check();
  await expect(page.locator("#dash-results table")).toBeVisible();
  await expect(page).not.toHaveURL(/view=grid/);

  // Select the page, then delete after the confirm dialog.
  await page.locator("[data-check-all]").check();
  await expect(page.locator("[data-selcount]")).toHaveText("3 selected");
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: "Delete" }).click();
  const del = page.locator("#dash-delete");
  await expect(del.getByRole("heading")).toHaveText("Delete 3 items?");
  await del.getByRole("button", { name: "Delete" }).click();
  await expect(page.locator("[data-dash-notice]")).toContainText("Deleted 3 items.");
  await expect(page.locator("#dash-results tbody tr[data-row]")).toHaveCount(0);
  expect(errors).toEqual([]);
});
