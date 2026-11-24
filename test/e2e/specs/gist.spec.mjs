import { test, expect } from "@playwright/test";

// A gist made in the composer against the real server: the share page shows
// every file, a file's raw link is plain text in a sandbox, and the zip
// holds the files.
//
// Needs a verified account: E2E_USER and E2E_PASSWORD sign in through /login,
// or COOKIE="name=value" supplies a session directly.
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
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
  }
  await page.goto("/#panel-paste");
  await expect(page.getByRole("tab", { name: "Paste" })).toHaveAttribute("aria-selected", "true");
}

test("a gist from the Paste tab opens with every file, raw as text, and as a zip", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  await signIn(page);
  const form = page.locator("[data-paste-form]");
  await form.getByRole("button", { name: "Add file" }).click();
  const blocks = form.locator("[data-snippet]");
  // A copied block's language combobox opens its own listbox, not the first block's.
  const lists = blocks.locator(".pb-listbox");
  const [id0, id1] = [await lists.nth(0).getAttribute("id"), await lists.nth(1).getAttribute("id")];
  expect(id0).not.toBe(id1);
  await expect(blocks.nth(1).locator("[role=combobox]")).toHaveAttribute("aria-controls", id1);
  await blocks.nth(1).locator("[data-combo-toggle]").dispatchEvent("mousedown");
  await expect(lists.nth(1)).toBeVisible();
  await expect(lists.nth(0)).toBeHidden();
  await blocks.nth(1).locator(".pb-option:not(.pb-option--empty)").first().click();
  await expect(blocks.nth(1).locator("[role=combobox]")).not.toHaveValue("");
  await expect(blocks.nth(0).locator("[role=combobox]")).toHaveValue("");
  await blocks.nth(1).locator("[role=combobox]").fill("");
  await form.locator('input[name="title"]').fill("e2e gist");
  await blocks.nth(0).locator('input[name="name"]').fill("main.go");
  await blocks.nth(0).locator('textarea[name="content"]').fill('package main\n\nfunc main() { println("<b>hi</b>") }\n');
  await blocks.nth(1).locator('input[name="name"]').fill("notes.md");
  await blocks.nth(1).locator('textarea[name="content"]').fill("# Notes\n\n<script>alert(1)</script>\n");
  await form.getByRole("button", { name: "Create gist" }).click();

  const link = form.locator("[data-result] a.pb-sharelink__url");
  await expect(link).toBeVisible();
  await page.goto(await link.getAttribute("href"));

  await expect(page.locator(".pb-media__title")).toHaveText("e2e gist");
  await expect(page.locator(".app-gist__name")).toHaveText(["main.go", "notes.md"]);
  // Code is escaped, Markdown sanitized: no element from either file's text.
  await expect(page.locator("#f0-src")).toContainText('println("<b>hi</b>")');
  await expect(page.locator(".app-gist .pb-code")).toHaveCount(2);
  await expect(page.locator(".app-gist b, .app-gist script")).toHaveCount(0);
  await expect(page.locator("#f1-rendered h1")).toHaveText("Notes");

  const raw = await page.request.get(await page.locator("#f0").getByRole("link", { name: "Raw" }).getAttribute("href"));
  expect(raw.status()).toBe(200);
  expect(raw.headers()["content-type"]).toBe("text/plain; charset=utf-8");
  expect(raw.headers()["x-content-type-options"]).toBe("nosniff");
  expect(raw.headers()["content-security-policy"]).toContain("sandbox");
  expect(await raw.text()).toContain("package main");

  const zip = await page.request.get(await page.getByRole("link", { name: "Download zip" }).getAttribute("href"));
  expect(zip.status()).toBe(200);
  expect(zip.headers()["content-type"]).toBe("application/zip");
  expect(zip.headers()["content-disposition"]).toContain("attachment");
  const body = await zip.body();
  expect(body.subarray(0, 4).toString("hex")).toBe("504b0304");
  expect(body.includes(Buffer.from("main.go"))).toBe(true);
  expect(body.includes(Buffer.from("notes.md"))).toBe(true);

  // A position past the last file is not found.
  const href = await page.locator("#f1").getByRole("link", { name: "Raw" }).getAttribute("href");
  expect((await page.request.get(href.replace("f=1", "f=2"))).status()).toBe(404);
  expect(errors).toEqual([]);
});
