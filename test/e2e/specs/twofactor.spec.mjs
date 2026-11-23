// Two-factor with an authenticator app: set it up from the secret on the
// setup page (codes made in node), sign in with the password and a code,
// and with a recovery code, which then doesn't work again.
//
// Needs the admin account from `make e2e-isolated` (E2E_USER / E2E_PASSWORD)
// to invite a fresh account; the admin's own sign-in is left alone.
import { test, expect } from "@playwright/test";
import { me, newMember, passwordStep, secondStep, setUpApp, signInAdmin, signOut, watchErrors } from "../twofactor.mjs";

test("an authenticator app and recovery codes as the second step", async ({ browser, request }) => {
  test.skip(!process.env.E2E_USER || !process.env.E2E_PASSWORD, "set E2E_USER and E2E_PASSWORD for an admin account");
  const adminCtx = await browser.newContext();
  const admin = await adminCtx.newPage();
  await signInAdmin(admin);
  const { ctx, page, username, password } = await newMember(browser, request, admin);
  await adminCtx.close();
  const errors = watchErrors(page);

  const { next, codes } = await setUpApp(page, password);
  await page.goto("/settings/security");
  await expect(page.locator("tr", { hasText: "Authenticator app" }).locator(".pb-badge")).toHaveText("on");
  await expect(page.locator("tr", { hasText: "Recovery codes" })).toContainText("10 left");
  await signOut(page);

  // The password alone isn't a sign-in.
  await passwordStep(page, username, password);
  await expect(me(page)).toHaveCount(0);
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/login\?next=/);

  await passwordStep(page, username, password);
  await secondStep(page, "totp", await next());
  await expect(me(page)).toContainText(username);
  await signOut(page);

  // A recovery code works once.
  await passwordStep(page, username, password);
  await secondStep(page, "recovery", codes[0]);
  await expect(me(page)).toContainText(username);
  await page.goto("/settings/security");
  await expect(page.locator("tr", { hasText: "Recovery codes" })).toContainText("9 left");
  await signOut(page);
  expect(errors).toEqual([]);

  // Used once, it is refused (a 401 page, which the console reports).
  await passwordStep(page, username, password);
  await secondStep(page, "recovery", codes[0]);
  await expect(page.getByText("That code didn't work.")).toBeVisible();
  await expect(me(page)).toHaveCount(0);
  await ctx.close();
});
