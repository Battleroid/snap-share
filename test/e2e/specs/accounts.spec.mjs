import { test, expect } from "@playwright/test";

// The admin is created by `make e2e-isolated` (snap-server admin create-user).
const ADMIN = { login: "admin", password: "e2e-admin-password" };
// Mailpit runs in the same private Docker network as the browser.
const MAILPIT = process.env.MAILPIT_URL || "http://mailpit:8025";

function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

async function signIn(page, login, password) {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(login);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Signed in as / })).toContainText(login);
}

async function signOut(page) {
  await page.getByRole("button", { name: /^Signed in as / }).click();
  await page.locator("#account-menu").getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
}

// Waits for a message to `to` and returns Mailpit's full message (Text, HTML, Subject).
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

test("admin signs in and out", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page, ADMIN.login, ADMIN.password);
  await expect(page.getByRole("navigation", { name: "Admin" }).getByRole("link", { name: "Overview" })).toBeVisible();
  await signOut(page);
  // The old session is gone: protected pages send us to sign in.
  await page.goto("/settings/profile");
  await expect(page).toHaveURL(/\/login\?next=/);
  expect(errors).toEqual([]);
});

test("wrong password is refused", async ({ page }) => {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(ADMIN.login);
  await page.locator('input[name="password"]').fill("not the password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByText("Wrong username or password.")).toBeVisible();
});

test("invite by email, register through it, sign in as the new user", async ({ page, browser, request }) => {
  const suffix = Date.now().toString(36);
  const username = `newbie-${suffix}`;
  const email = `${username}@e2e.invalid`;
  const password = "a new member password";

  await signIn(page, ADMIN.login, ADMIN.password);
  await page.goto("/admin/invites");
  await page.getByRole("button", { name: "New invite" }).first().click();
  const form = page.locator('form[action="/admin/invites"]');
  await form.locator('input[name="email"]').fill(email);
  await form.getByRole("button", { name: "Create invite" }).click();
  await expect(page.getByText(`Invite created and sent to ${email}`)).toBeVisible();
  await signOut(page);

  const mail = await waitForMail(request, email);
  expect(mail.Subject).toBe(`${ADMIN.login} invited you to snap`);
  expect(mail.Text).toContain("is a private place to share files, pastes, links and one-time secrets");
  expect(mail.Text).toContain(new URL(page.url()).origin);
  const link = mail.Text.match(/https:\/\/\S+\/invite#t=[A-Za-z0-9_-]{43}/)?.[0];
  expect(link, "invite link in the text body").toBeTruthy();
  expect(mail.HTML).toContain(link);

  // The invitee opens the link in their own browser.
  const ctx = await browser.newContext();
  const guest = await ctx.newPage();
  const errors = watchErrors(guest);
  const posted = [];
  guest.on("request", (r) => posted.push(r.url()));
  await guest.goto(link);
  await expect(guest.getByRole("heading", { name: "Create an account" })).toBeVisible();
  // The token never travelled in a URL the server sees.
  const token = link.split("#t=")[1];
  expect(posted.some((u) => u.includes(token))).toBeFalsy();
  expect(new URL(guest.url()).hash).toBe("");

  await expect(guest.locator('input[name="email"]')).toHaveValue(email);
  await guest.locator('input[name="username"]').fill(username);
  await guest.locator('input[name="password"]').fill(password);
  await guest.getByRole("button", { name: "Create account" }).click();
  await expect(guest.getByRole("button", { name: /^Signed in as / })).toContainText(username);

  // First run: a user with no items is welcomed once, and dismissing it sticks in this browser.
  await guest.goto("/");
  const welcome = guest.locator("[data-welcome]");
  await expect(welcome).toBeVisible();
  await expect(welcome).toContainText("Drop files to share");
  await expect(welcome.getByRole("link", { name: "Settings › Integrations" })).toHaveAttribute("href", "/settings/integrations");
  await expect(welcome).toContainText("keyboard shortcuts");
  await welcome.getByRole("button", { name: "Dismiss" }).click();
  await expect(welcome).toHaveCount(0);
  await guest.reload();
  await expect(guest.getByRole("tab", { name: "Upload" })).toBeVisible();
  await expect(guest.locator("[data-welcome]")).toHaveCount(0);
  await signOut(guest);
  expect(errors).toEqual([]);

  await signIn(guest, username, password);
  await guest.goto("/settings/security");
  await expect(guest.getByText("this device", { exact: true })).toBeVisible();
  await ctx.close();
});

const DEAD = "A".repeat(43); // shaped like a token, matches nothing

test("a dead reset link says so before a password is typed", async ({ page }) => {
  const errors = watchErrors(page);
  const checks = [];
  page.on("request", (r) => { if (r.url().endsWith("/reset/check")) checks.push(r.method()); });
  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto(`/reset#t=${DEAD}`);
  const result = page.locator(".pb-result--compact");
  await expect(result.locator(".pb-result__title")).toHaveText("This link has expired or been used");
  expect(new URL(page.url()).hash).toBe("");
  await expect(page.locator('input[name="password"]')).toBeHidden(); // no form to type into
  expect(checks).toEqual(["POST"]);
  await result.getByRole("link", { name: "Request a new link" }).click();
  await expect(page.getByRole("button", { name: "Send link" })).toBeVisible();
  expect(errors.filter((e) => !e.startsWith("Failed to load resource"))).toEqual([]);
});

test("a dead verify and invite link are results with a way out", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto(`/verify#t=${DEAD}`);
  const result = page.locator(".pb-result--compact[data-status=down]");
  await expect(result.locator(".pb-result__title")).toHaveText("This link can't be used");
  await expect(result.locator(".pb-result__body")).toContainText("expired or has been used");
  await expect(result.getByRole("link", { name: /^Go to / })).toBeFocused();
  await page.goto(`/invite#t=${DEAD}`);
  await expect(page.locator(".pb-result--compact[data-status=down] .pb-result__title")).toHaveText("This invite can't be used");
  await expect(page.getByRole("link", { name: "Back to sign in" }).first()).toBeVisible();
  // No token at all is an incomplete link, also a result.
  await page.goto("/verify");
  await expect(page.locator(".pb-result--compact[data-status=warn] .pb-result__title")).toHaveText("This link is incomplete");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
