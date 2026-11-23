// Helpers for the two-factor specs: an RFC 6238 authenticator in node,
// accounts made through an emailed invite, and the sign-in steps.
import { createHmac } from "node:crypto";
import { expect } from "@playwright/test";

// Mailpit runs in the same private Docker network as the browser.
const MAILPIT = process.env.MAILPIT_URL || "http://mailpit:8025";

export function base32Decode(s) {
  const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const out = [];
  let bits = 0;
  let value = 0;
  for (const c of s.replace(/=+$/, "").toUpperCase()) {
    value = ((value << 5) | alpha.indexOf(c)) & 0xffff;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

// totp is the RFC 6238 code of secret at a 30-second step (HMAC-SHA1, 6 digits).
export function totp(secret, step) {
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(step));
  const sum = createHmac("sha1", secret).update(msg).digest();
  const off = sum[sum.length - 1] & 0x0f;
  return String((sum.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

// authenticator returns a function that gives a code for a step no earlier
// code used (each works once): the current step, or the next one, waiting
// when both are used.
export function authenticator(secret) {
  let last = -1;
  return async () => {
    for (;;) {
      const now = Math.floor(Date.now() / 30_000);
      const step = Math.max(now, last + 1);
      if (step <= now + 1) {
        last = step;
        return totp(secret, step);
      }
      await new Promise((r) => setTimeout(r, 1000));
    }
  };
}

export function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

const me = (page) => page.locator(".app-user button[popovertarget]");

// signInAdmin signs in as the stack's admin. Other spec files sign in as
// the same admin; a burst of sign-ins can trip the per-account limit.
export async function signInAdmin(page) {
  const busy = page.getByText(/^Too many attempts/);
  for (let i = 0; i < 5; i++) {
    await page.goto("/login");
    await page.locator('input[name="login"]').fill(process.env.E2E_USER);
    await page.locator('input[name="password"]').fill(process.env.E2E_PASSWORD);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(me(page).or(busy)).toBeVisible();
    if (await me(page).isVisible()) return;
    await page.waitForTimeout(2000);
  }
  throw new Error("sign-in kept hitting the rate limit");
}

async function waitForMail(request, to) {
  for (let i = 0; i < 60; i++) {
    const res = await request.get(`${MAILPIT}/api/v1/messages`);
    expect(res.ok()).toBeTruthy();
    const { messages = [] } = await res.json();
    const hit = messages.find((m) => (m.To || []).some((t) => t.Address === to));
    if (hit) {
      const full = await request.get(`${MAILPIT}/api/v1/message/${hit.ID}`);
      return full.json();
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`no mail to ${to}`);
}

// newMember invites a new account from adminPage and registers it in its
// own browser context. It returns the context, its page and the account.
export async function newMember(browser, request, adminPage, role = "user") {
  const username = `tf-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  const email = `${username}@e2e.invalid`;
  const password = "a two-factor member password";
  await adminPage.goto("/admin/invites");
  const form = adminPage.locator('form[action="/admin/invites"]');
  await form.locator('input[name="email"]').fill(email);
  await form.locator('select[name="role"]').selectOption(role);
  await form.getByRole("button", { name: "Create invite" }).click();
  await expect(adminPage.getByText(`Invite created and sent to ${email}`)).toBeVisible();
  const mail = await waitForMail(request, email);
  const link = mail.Text.match(/https:\/\/\S+\/invite#t=[A-Za-z0-9_-]{43}/)?.[0];
  expect(link, "invite link").toBeTruthy();

  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(link);
  await page.locator('input[name="username"]').fill(username);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(me(page)).toContainText(username);
  return { ctx, page, username, password };
}

// setUpApp turns on an authenticator app in Settings › Security, reading
// the secret from the setup page. proof gives a code for confirm it's you
// when the account has a factor already. It returns the authenticator and
// the recovery codes shown.
export async function setUpApp(page, password, proof) {
  await page.goto("/settings/security");
  await page.getByRole("button", { name: /^(Set up|Replace)…$/ }).click();
  const dialog = page.locator("#totp-setup");
  await dialog.locator('input[name="current"]').fill(password);
  if (proof) await dialog.locator('input[name="code"]').fill(await proof());
  await dialog.getByRole("button", { name: "Continue" }).click();
  await expect(page.locator("img.app-qr")).toBeVisible();
  const secret = await page.locator("#totp-secret").inputValue();
  expect(secret).toMatch(/^[A-Z2-7]{32}$/);
  const next = authenticator(base32Decode(secret));
  await page.locator('input[name="code"]').fill(await next());
  await page.getByRole("button", { name: "Turn on" }).click();
  await expect(page.getByText("Save your recovery codes")).toBeVisible();
  const codes = (await page.locator("#recovery-codes").textContent()).trim().split(/\s+/);
  expect(codes).toHaveLength(10);
  return { next, codes };
}

export async function signOut(page) {
  await me(page).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
}

// passwordStep signs in with the password, which leads to the second step.
export async function passwordStep(page, username, password) {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(username);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page).toHaveURL(/\/login\/2fa$/);
}

// secondStep enters an authenticator code (kind "totp") or a recovery code.
export async function secondStep(page, kind, code) {
  if (kind === "recovery") await page.locator("summary", { hasText: "Use a recovery code" }).click();
  const form = page.locator(`form[action="/login/2fa/code"]:has(input[name="kind"][value="${kind}"])`);
  await form.locator('input[name="code"]').fill(code);
  await form.getByRole("button", { name: "Continue" }).click();
}

export { me };
