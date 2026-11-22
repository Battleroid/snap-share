// Admin area: create a user, set a quota, disable them; toggle a setting
// that applies at once; burn a secret by pasting its link.
//
// Needs the admin account from `make e2e-isolated` (E2E_USER / E2E_PASSWORD).
import { test, expect } from "@playwright/test";

// The stats classifier drops HeadlessChrome; look like a normal browser.
const UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";

const LINK = /\/s\/[A-Za-z0-9_-]{22}#[A-Za-z0-9_-]{43}$/;

async function signIn(page) {
  const user = process.env.E2E_USER;
  // Other spec files sign in as the same admin; a burst of sign-ins can trip
  // the per-account limit until one of them succeeds.
  const me = page.locator(".app-user button[popovertarget]");
  const busy = page.getByText(/^Too many attempts/);
  for (let i = 0; i < 5; i++) {
    await page.goto("/login");
    await page.locator('input[name="login"]').fill(user);
    await page.locator('input[name="password"]').fill(process.env.E2E_PASSWORD);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(me.or(busy)).toBeVisible();
    if (await me.isVisible()) {
      await expect(me).toContainText(user);
      return;
    }
    await page.waitForTimeout(2000);
  }
  throw new Error("sign-in kept hitting the rate limit");
}

// One signed-in page for the whole file: a single sign-in, made after the
// other files' first ones, stays clear of that limit.
test.describe.configure({ mode: "serial" });
let page;
let errors = [];
test.beforeAll(async ({ browser }) => {
  test.skip(!process.env.E2E_USER || !process.env.E2E_PASSWORD, "set E2E_USER and E2E_PASSWORD for an admin account");
  const ctx = await browser.newContext({
    baseURL: process.env.BASE_URL, ignoreHTTPSErrors: true, userAgent: UA, viewport: { width: 1280, height: 900 },
  });
  page = await ctx.newPage();
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(m.text());
  });
  await page.waitForTimeout(3000);
  await signIn(page);
});
test.beforeEach(() => { errors = []; });
test.afterAll(async () => { await page?.context().close(); });

test("overview shows the site at a glance", async () => {
  await page.goto("/");
  await page.getByRole("link", { name: "admin", exact: true }).click();
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.locator(".pb-tile")).toHaveCount(5);
  await expect(page.locator(".pb-tile__label", { hasText: "users · active" })).toBeVisible();
  for (const tab of ["Overview", "Users", "Invites", "Content", "Secrets", "Settings", "Audit"]) {
    await expect(page.getByRole("navigation", { name: "Admin" }).getByRole("link", { name: tab })).toBeVisible();
  }
  expect(errors).toEqual([]);
});

test("create a user, set a quota, disable them", async () => {
  const name = `qa-${Date.now().toString(36)}`;

  await page.goto("/admin/users");
  await page.getByRole("link", { name: "New user" }).click();
  await page.locator('input[name="username"]').fill(name);
  await page.locator('input[name="email"]').fill(`${name}@e2e.invalid`);
  await page.getByRole("button", { name: "Create user" }).click();
  // The e2e stack has SMTP (Mailpit), so the set-password link is emailed.
  await expect(page.getByText(`Created ${name}. A link to set a password was sent to ${name}@e2e.invalid.`)).toBeVisible();
  await expect(page.getByRole("heading", { name })).toBeVisible();

  await page.locator('input[name="quota"]').fill("10 MB");
  await page.getByRole("button", { name: "Save limits" }).click();
  await expect(page.getByText("Limits saved.")).toBeVisible();
  await expect(page.locator('input[name="quota"]')).toHaveValue("10 MB");
  await expect(page.getByRole("cell", { name: /of 10 MB$/ })).toBeVisible();

  await page.getByRole("button", { name: "Disable" }).click();
  const dialog = page.getByRole("dialog", { name: `Disable ${name}?` });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Disable" }).click();
  await expect(page.getByText("User disabled.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Enable" })).toBeVisible();

  await page.goto(`/admin/users?q=${name}`);
  const row = page.locator(`tr[data-user="${name}"]`);
  await expect(row).toBeVisible();
  await expect(row.locator('.pb-badge[data-status="paused"]')).toHaveText("disabled");
  await expect(row.getByRole("cell", { name: "0 B / 10 MB" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("a switch applies at once, and the password sign-in guard holds", async () => {
  await page.goto("/admin/settings");

  const teasers = page.locator('input[data-setting="media.teasers"]');
  const was = await teasers.isChecked();
  const saved = page.waitForResponse((r) => r.url().endsWith("/admin/settings/apply") && r.status() === 200);
  await teasers.click();
  await saved;
  await expect(page.locator("#set-media-teasers-help")).toHaveText("Saved.");
  await page.reload();
  await expect(teasers).toBeChecked({ checked: !was });
  // Put it back.
  const back = page.waitForResponse((r) => r.url().endsWith("/admin/settings/apply") && r.status() === 200);
  await teasers.click();
  await back;
  await page.reload();
  await expect(teasers).toBeChecked({ checked: was });

  // No SSO on this stack: password sign-in cannot be switched off.
  const pw = page.locator('input[data-setting="auth.password_login"]');
  await expect(pw).toBeChecked();
  await pw.click();
  await expect(page.locator("#set-auth-password_login-help")).toContainText("nobody could sign in");
  await expect(pw).toBeChecked();
  await page.reload();
  await expect(pw).toBeChecked();
  expect(errors).toEqual([]);
});

test("burn a secret by its link; the key never leaves the browser", async ({ browser }) => {
  await page.goto("/new/secret");
  await page.locator('textarea[name="text"]').fill("burn me before reading");
  await page.getByRole("button", { name: "Create secret" }).click();
  const field = page.locator("#secret-link");
  await expect(field).toHaveValue(LINK, { timeout: 20_000 });
  const link = await field.inputValue();
  const key = link.split("#")[1];

  const posted = [];
  const record = (r) => posted.push(`${r.url()} ${r.postData() || ""}`);
  page.on("request", record);
  await page.goto("/admin/secrets");
  await page.locator('form[data-burn-form] input[name="ref"]').fill(link);
  await page.getByRole("button", { name: "Burn…" }).click();
  const dialog = page.getByRole("dialog", { name: "Burn this secret?" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Burn" }).click();
  await expect(page.getByText("Secret burned. Its link no longer works.")).toBeVisible();
  page.off("request", record);
  expect(posted.some((p) => p.includes("/admin/secrets/burn"))).toBeTruthy();
  expect(posted.some((p) => p.includes(key) || p.includes(encodeURIComponent(key)))).toBeFalsy();
  expect(errors).toEqual([]);

  // The link holder now finds a tombstone.
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, userAgent: UA });
  const guest = await ctx.newPage();
  await guest.goto(link);
  await expect(guest.getByText("Revoked by the sender")).toBeVisible();
  await expect(guest.getByRole("button", { name: "Reveal" })).toBeHidden();
  await ctx.close();
});
