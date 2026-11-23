// auth.require_2fa = everyone, set on Admin › Settings: an account without
// a second factor reaches only the setup until it has one.
//
// The policy covers every account on the stack, so this file runs after all
// the others (the "last" project in playwright.config.mjs), by an admin who
// has an authenticator app, and puts the setting back at the end.
import { test, expect } from "@playwright/test";
import { me, newMember, setUpApp, signInAdmin, watchErrors } from "../twofactor.mjs";

async function setPolicy(page, value) {
  await page.goto("/admin/settings");
  const form = page.locator("form", { has: page.locator('select[name="auth.require_2fa"]') });
  await form.locator('select[name="auth.require_2fa"]').selectOption(value);
  await form.getByRole("button", { name: "Save" }).click();
  await expect(page.locator('select[name="auth.require_2fa"]')).toHaveValue(value);
}

test("policy everyone forces the setup first", async ({ browser, request }) => {
  test.skip(!process.env.E2E_USER || !process.env.E2E_PASSWORD, "set E2E_USER and E2E_PASSWORD for an admin account");
  const adminCtx = await browser.newContext();
  const admin = await adminCtx.newPage();
  await signInAdmin(admin);
  // Both accounts are made while the stack's admin can still use the site.
  const boss = await newMember(browser, request, admin, "admin");
  const worker = await newMember(browser, request, admin);
  await adminCtx.close();
  const errors = watchErrors(worker.page);
  await setUpApp(boss.page, boss.password);

  await setPolicy(boss.page, "everyone");
  try {
    // An existing session is held at the setup at once.
    await worker.page.goto("/dashboard");
    await expect(worker.page).toHaveURL(/\/settings\/security(#two-factor)?$/);
    await expect(worker.page.getByText("Set up two-factor authentication to continue.")).toBeVisible();
    await expect(worker.page.getByRole("navigation", { name: "Main" })).toHaveCount(0);
    // Public pages treat it as signed out.
    await worker.page.goto("/");
    await expect(me(worker.page)).toHaveCount(0);

    await setUpApp(worker.page, worker.password);
    await expect(worker.page.getByRole("navigation", { name: "Main" })).toBeVisible();
    await worker.page.goto("/dashboard");
    await expect(worker.page).toHaveURL(/\/dashboard$/);
    await expect(me(worker.page)).toContainText(worker.username);
  } finally {
    await setPolicy(boss.page, "off");
  }
  expect(errors).toEqual([]);
  await boss.ctx.close();
  await worker.ctx.close();
});
