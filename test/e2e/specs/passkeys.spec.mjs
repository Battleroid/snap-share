// Passkeys with Chromium's virtual authenticator (CDP): add one in Settings,
// sign in with it alone (autofill, then the button), use it after the
// password, remove it.
//
// Needs the admin account from `make e2e-isolated` (E2E_USER / E2E_PASSWORD)
// to create a user of its own: the shared accounts must not get a factor.
import { test, expect } from "@playwright/test";

// Mailpit runs in the same private Docker network as the browser.
const MAILPIT = process.env.MAILPIT_URL || "http://mailpit:8025";
const PASSWORD = "a passkey user password";

function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

// Other spec files sign in as the same admin; a burst of sign-ins can trip
// the per-account limit until one of them succeeds.
async function signInAdmin(page) {
  const me = page.locator(".app-user button[popovertarget]");
  const busy = page.getByText(/^Too many attempts/);
  for (let i = 0; i < 5; i++) {
    await page.goto("/login");
    await page.locator('input[name="login"]').fill(process.env.E2E_USER);
    await page.locator('input[name="password"]').fill(process.env.E2E_PASSWORD);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(me.or(busy)).toBeVisible();
    if (await me.isVisible()) return;
    await page.waitForTimeout(2000);
  }
  throw new Error("sign-in kept hitting the rate limit");
}

async function signOut(page) {
  await page.locator(".app-user button[popovertarget]").click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
}

async function waitForMail(request, to) {
  for (let i = 0; i < 60; i++) {
    const res = await request.get(`${MAILPIT}/api/v1/messages`);
    expect(res.ok()).toBeTruthy();
    const { messages = [] } = await res.json();
    const hit = messages.find((m) => (m.To || []).some((t) => t.Address === to));
    if (hit) {
      const full = await request.get(`${MAILPIT}/api/v1/message/${hit.ID}`);
      expect(full.ok()).toBeTruthy();
      return full.json();
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`no mail to ${to}`);
}

// A platform authenticator that holds discoverable credentials, verifies
// its user and approves every request. Added before the page navigates.
async function virtualAuthenticator(page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2", transport: "internal", hasResidentKey: true,
      hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
    },
  });
  return async () => (await cdp.send("WebAuthn.getCredentials", { authenticatorId })).credentials;
}

test("add a passkey, sign in with it, use it as the second step, remove it", async ({ browser, request }) => {
  test.skip(!process.env.E2E_USER || !process.env.E2E_PASSWORD, "set E2E_USER and E2E_PASSWORD for an admin account");
  test.setTimeout(120_000);
  const name = `pk-${Date.now().toString(36)}`;
  const email = `${name}@e2e.invalid`;

  await test.step("an admin creates the user", async () => {
    const ctx = await browser.newContext();
    const admin = await ctx.newPage();
    await signInAdmin(admin);
    await admin.goto("/admin/users");
    await admin.getByRole("link", { name: "New user" }).click();
    await admin.locator('input[name="username"]').fill(name);
    await admin.locator('input[name="email"]').fill(email);
    await admin.getByRole("button", { name: "Create user" }).click();
    await expect(admin.getByText(`Created ${name}.`, { exact: false })).toBeVisible();
    await ctx.close();
  });

  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const credentials = await virtualAuthenticator(page);
  const errors = watchErrors(page);
  const me = page.locator(".app-user button[popovertarget]");

  await test.step("the user sets a password from the emailed link", async () => {
    const mail = await waitForMail(request, email);
    const link = mail.Text.match(/https:\/\/\S+\/reset#t=[A-Za-z0-9_-]{43}/)?.[0];
    expect(link, "set-password link").toBeTruthy();
    await page.goto(link);
    await page.locator('input[name="password"]').fill(PASSWORD);
    await page.getByRole("button", { name: "Set password" }).click();
    await expect(me).toContainText(name);
  });

  await test.step("add a passkey in Settings", async () => {
    await page.goto("/settings/security");
    await expect(page.getByText("No passkeys yet.")).toBeVisible();
    await page.getByRole("button", { name: "Add a passkey" }).click();
    const dialog = page.getByRole("dialog", { name: "Add a passkey" });
    await dialog.getByLabel("Current password").fill(PASSWORD);
    await dialog.getByRole("button", { name: "Continue" }).click();
    // The first factor: the recovery codes, shown once.
    await expect(page.getByText("Save your recovery codes")).toBeVisible();
    await expect(page.locator("#recovery-codes")).toHaveText(/^([0-9a-z]{4}-){3}[0-9a-z]{4}\n/);
    await expect(page.getByRole("cell", { name: "Passkey", exact: true })).toBeVisible();
    const creds = await credentials();
    expect(creds).toHaveLength(1);
    expect(creds[0].isResidentCredential).toBe(true);
    expect(creds[0].rpId).toBe(new URL(process.env.BASE_URL).hostname);
  });

  await test.step("sign in through autofill", async () => {
    // The sign-in page asks for a passkey in the login field's autofill; the
    // virtual authenticator answers it at once.
    const finished = page.waitForResponse((r) => r.url().endsWith("/login/passkey/finish"));
    await page.locator(".app-user button[popovertarget]").click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    expect((await finished).status()).toBe(200);
    await expect(me).toContainText(name);
    await page.goto("/settings/security");
    await expect(page.getByRole("row", { name: /Passkey/ }).getByRole("cell").nth(2)).not.toHaveText("—");
  });

  await test.step("sign in with the button", async () => {
    // From here on the browser offers no autofill, so only the button can ask.
    await page.addInitScript(() => {
      if (globalThis.PublicKeyCredential) PublicKeyCredential.isConditionalMediationAvailable = async () => false;
    });
    await signOut(page);
    await page.getByRole("button", { name: "Sign in with a passkey" }).click();
    await expect(me).toContainText(name);
  });

  await test.step("the passkey is the second step after the password", async () => {
    await signOut(page);
    await page.locator('input[name="login"]').fill(name);
    await page.locator('input[name="password"]').fill(PASSWORD);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page).toHaveURL(/\/login\/2fa$/);
    await expect(me).toHaveCount(0);
    await page.getByRole("button", { name: "Use a passkey or security key" }).click();
    await expect(me).toContainText(name);
  });

  await test.step("remove it, confirmed with the password and the passkey", async () => {
    await page.goto("/settings/security");
    await page.getByRole("button", { name: "Remove…" }).click();
    const dialog = page.getByRole("dialog", { name: "Remove Passkey?" });
    await dialog.getByLabel("Current password").fill(PASSWORD);
    await dialog.getByRole("button", { name: "Use a passkey" }).click();
    await expect(page).toHaveURL(/saved=passkey_removed/);
    await expect(page.getByText("No passkeys yet.")).toBeVisible();
    await expect(page.getByText("Passkey removed.")).toBeVisible();
    // The password alone signs in again.
    await signOut(page);
    await page.locator('input[name="login"]').fill(name);
    await page.locator('input[name="password"]').fill(PASSWORD);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(me).toContainText(name);
  });

  expect(errors).toEqual([]);
  await ctx.close();
});
