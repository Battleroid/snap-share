// Admin area: create a user, set a quota, disable them; an invite from
// creation to removal; toggle a setting that applies at once; burn a secret
// by pasting its link.
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
  const me = page.getByRole("button", { name: /^Signed in as / });
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
  await page.getByRole("navigation", { name: "Admin" }).getByRole("link", { name: "Overview" }).click();
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
  await expect(page.locator(".pb-dl dd", { hasText: /of 10 MB$/ })).toBeVisible();

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
  await expect(row.locator('[title="0 B of 10 MB"]')).toBeVisible();
  expect(errors).toEqual([]);
});

test("a labelled invite: copy its link again, revoke it, remove it", async () => {
  const label = `qa invite ${Date.now().toString(36)}`;
  await page.goto("/admin/invites");
  await page.getByRole("button", { name: "New invite" }).first().click();
  const form = page.locator('form[action="/admin/invites"]');
  await form.locator('input[name="label"]').fill(label);
  await form.getByRole("button", { name: "Create invite" }).click();
  await expect(page.getByText("Invite created", { exact: true })).toBeVisible();
  const created = await page.locator("#invite-link").inputValue();
  expect(created).toMatch(/\/invite#t=[A-Za-z0-9_-]{43}$/);

  // The list never carries the link; Copy link asks for it.
  await page.goto("/admin/invites");
  expect(await page.content()).not.toContain(created.split("#")[1]);
  const row = page.locator("tr", { hasText: label });
  await expect(row.getByRole("cell", { name: "link", exact: true })).toBeVisible();
  await row.getByRole("button", { name: `Actions for ${label}` }).click();
  await page.getByRole("menuitem", { name: "Copy link" }).click();
  const dialog = page.getByRole("dialog", { name: `Invite link · ${label}` });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator("#invite-link-again")).toHaveValue(created);
  await dialog.getByRole("button", { name: "Done" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator("#invite-link-again")).toHaveValue("");

  await row.getByRole("button", { name: `Actions for ${label}` }).click();
  await page.getByRole("menuitem", { name: "Revoke…" }).click();
  const revoke = page.getByRole("dialog", { name: "Revoke this invite?" });
  await revoke.getByRole("button", { name: "Revoke invite" }).click();
  await expect(page.getByText("Invite revoked.")).toBeVisible();
  await expect(page.locator("tr", { hasText: label })).toHaveCount(0);

  // Finished invites are under all, where they can be removed.
  await page.locator(".pb-seg label").filter({ hasText: /^all$/ }).click();
  await expect(page).toHaveURL(/show=all/);
  const done = page.locator("tr", { hasText: label });
  await expect(done).toContainText("revoked");
  await done.getByRole("button", { name: `Actions for ${label}` }).click();
  await page.getByRole("menuitem", { name: "Remove…" }).click();
  const remove = page.getByRole("dialog", { name: "Remove this invite?" });
  await remove.getByRole("button", { name: "Remove invite" }).click();
  await expect(page.getByText("Invite removed.")).toBeVisible();
  await expect(page).toHaveURL(/show=all/);
  await expect(page.locator("tr", { hasText: label })).toHaveCount(0);
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

test("the users row menu asks before Reset password and Disable", async () => {
  const name = `qm-${Date.now().toString(36)}`;
  await page.goto("/admin/users/new");
  await page.locator('input[name="username"]').fill(name);
  await page.locator('input[name="email"]').fill(`${name}@e2e.invalid`);
  await page.getByRole("button", { name: "Create user" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();

  await page.goto(`/admin/users?q=${name}`);
  const row = page.locator(`tr[data-user="${name}"]`);
  await row.getByRole("button", { name: `Actions for ${name}` }).click();
  await page.getByRole("menuitem", { name: "Reset password…" }).click();
  const reset = page.getByRole("dialog", { name: `Reset password for ${name}?` });
  await expect(reset).toBeVisible();
  await reset.getByRole("button", { name: "Cancel" }).click();
  await expect(reset).toBeHidden();
  await expect(page).toHaveURL(/\/admin\/users\?q=/); // nothing was sent

  await row.getByRole("button", { name: `Actions for ${name}` }).click();
  await page.getByRole("menuitem", { name: "Disable…" }).click();
  const disable = page.getByRole("dialog", { name: `Disable ${name}?` });
  await expect(disable).toBeVisible();
  await disable.getByRole("button", { name: "Disable user" }).click();
  await expect(page.getByText("User disabled.")).toBeVisible();
  await expect(row.locator('.pb-badge[data-status="paused"]')).toHaveText("disabled");
  expect(errors).toEqual([]);
});

test("a form error shows on the field and takes focus", async () => {
  await page.goto("/admin/users/new");
  await page.locator('input[name="username"]').fill("not a valid name!");
  await page.getByRole("button", { name: "Create user" }).click();
  const input = page.locator('input[name="username"]');
  await expect(input).toHaveAttribute("aria-invalid", "true");
  const field = page.locator(".pb-field", { has: input });
  await expect(field.locator(".pb-field__error")).toBeVisible();
  await expect(input).toBeFocused();
  expect(errors).toEqual([]);
});

test("API keys hide revoked ones behind active and all", async () => {
  const name = `rk-${Date.now().toString(36)}`;
  await page.goto("/settings/keys/new");
  await page.locator('input[name="name"]').fill(name);
  await page.getByRole("button", { name: "Create key" }).click();
  await page.goto("/settings/keys");
  const row = page.locator("tr", { hasText: name });
  await expect(row.locator(".app-keyid")).toContainText("snap_"); // the id sits under the name
  await row.getByRole("button", { name: `Actions for ${name}` }).click();
  await page.getByRole("menuitem", { name: "Revoke…" }).click();
  await page.getByRole("dialog", { name: `Revoke ${name}?` }).getByRole("button", { name: "Revoke key" }).click();
  await expect(page.getByText("Key revoked.")).toBeVisible();
  await expect(page.locator("tr", { hasText: name })).toHaveCount(0);
  // "all" brings it back, muted and marked.
  await page.getByRole("radio", { name: "all" }).check({ force: true });
  await expect(page).toHaveURL(/show=all/);
  const back = page.locator("tr", { hasText: name });
  await expect(back).toHaveAttribute("data-key-status", "revoked");
  await expect(back.getByRole("button", { name: /Actions for/ })).toHaveCount(0);
  for (const w of [1280, 1120, 390]) {
    await page.setViewportSize({ width: w, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    expect(await page.evaluate(() => [...document.querySelectorAll(".pb-tablescroll")].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  expect(errors).toEqual([]);
});
