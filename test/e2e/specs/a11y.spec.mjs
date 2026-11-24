import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// Accessibility smoke test: axe (WCAG 2.2 A and AA, serious and critical) on the pages people use most, signed in
// and signed out, at desktop and phone width; and one keyboard-only path through create → copy → manage → delete.
// Needs the admin account from `make e2e-isolated` (E2E_USER / E2E_PASSWORD).
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
test.skip(!user || !password, "set E2E_USER and E2E_PASSWORD for an admin account");

async function signIn(page) {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(user);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Upload" })).toBeVisible();
}

async function axe(page, label) {
  // The kit's DualRange lays two native range inputs over one track on purpose; axe reads them as overlapping targets.
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).exclude(".pb-range2 .pb-range").analyze();
  const bad = r.violations.filter((v) => ["serious", "critical"].includes(v.impact));
  if (process.env.A11Y_DEBUG) console.log(`A11Y ${label} passes=${r.passes.length} violations=${r.violations.map((v) => v.id + ":" + v.impact).join(",")}`);
  const report = bad.map((v) => `${label}: ${v.id} (${v.impact}) ${v.help}\n  ${v.nodes.slice(0, 3).map((n) => n.target.join(" ")).join("\n  ")}`).join("\n");
  expect(bad, report).toEqual([]);
}

// A 2x1 PNG.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4zwAE/wEHAAH/4iOeWQAAAABJRU5ErkJggg==";

async function seed(page) {
  const up = await page.request.put(`/api/v1/upload/a11y-${Date.now().toString(36)}.png`, { data: Buffer.from(PNG, "base64"), headers: { Accept: "application/json" } });
  expect(up.status()).toBe(201);
  const item = await up.json();
  const paste = await page.request.post("/api/v1/pastes", { data: "package main\n\nfunc main() {}\n", headers: { "Content-Type": "text/plain", Accept: "application/json" } });
  expect(paste.status()).toBe(201);
  return { item, paste: await paste.json() };
}

for (const width of [1280, 390]) {
  test(`axe, signed in, ${width}px`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    const { item } = await seed(page);
    const pages = ["/", "/#panel-paste", "/#panel-secret", "/dashboard", "/dashboard?view=grid", "/dashboard?type=album", "/dashboard/secrets",
      "/dashboard/stats", "/notifications", "/settings/profile", "/settings/security", "/settings/keys", "/settings/defaults",
      "/settings/integrations", "/settings/notifications", "/admin", "/admin/users", "/admin/invites", "/admin/content", "/admin/settings",
      "/admin/audit", new URL(item.url).pathname];
    for (const path of pages) {
      await page.goto(path);
      await page.waitForLoadState("networkidle");
      await axe(page, path);
    }
  });

  test(`axe, signed out, ${width}px`, async ({ page, browser }) => {
    test.setTimeout(120_000);
    await signIn(page);
    const { item, paste } = await seed(page);
    const anon = await browser.newContext({ viewport: { width, height: 900 } });
    const p = await anon.newPage();
    for (const path of ["/login", "/reset", new URL(item.url).pathname, new URL(paste.url).pathname, "/does-not-exist", "/x"]) {
      await p.goto(path);
      await p.waitForLoadState("networkidle");
      await axe(p, `signed out ${path}`);
    }
    await anon.close();
  });
}

test("keyboard only: paste, copy its link, find it, delete it", async ({ page }) => {
  test.setTimeout(90_000);
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await signIn(page);
  const name = `kb-${Date.now().toString(36)}.txt`;
  // Paste: Ctrl/⌘+Enter creates from the text box.
  await page.goto("/#panel-paste");
  await page.getByRole("tab", { name: "Paste" }).focus();
  await page.keyboard.press("Enter");
  await page.locator('#panel-paste input[name="name"]').focus();
  await page.keyboard.type(name);
  await page.locator("#panel-paste textarea").first().focus();
  await page.keyboard.type("hello from the keyboard");
  await page.keyboard.press("ControlOrMeta+Enter");
  const copy = page.locator("[data-filelist], #panel-paste").getByRole("button", { name: "Copy link" }).first();
  await expect(copy).toBeVisible();
  await copy.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Copied" }).first()).toBeVisible();
  // Find it: / focuses the search on Items.
  await page.goto("/dashboard");
  await page.keyboard.press("/");
  await expect(page.locator("main input[name=q]")).toBeFocused();
  await page.keyboard.type(name.slice(0, 8));
  const row = page.locator("tr", { hasText: name });
  await expect(row).toHaveCount(1);
  // Its row menu, by keyboard, then Delete…, which asks first with focus on Cancel.
  await row.getByRole("button", { name: /More|Actions/ }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("menuitem", { name: /Delete/ }).first()).toBeVisible();
  await page.getByRole("menuitem", { name: /Delete/ }).first().focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(row).toHaveCount(1);
});
