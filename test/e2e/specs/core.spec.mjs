import { test, expect } from "@playwright/test";

test("sign-in page renders with no CSP violations", async ({ page }) => {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.locator(".pb-icon").first()).toBeVisible();
  expect(errors).toEqual([]);
});

test("password reveal toggles the input type", async ({ page }) => {
  await page.goto("/login");
  const input = page.locator('input[name="password"]');
  await expect(input).toHaveAttribute("type", "password");
  await page.getByRole("button", { name: "Show password" }).click();
  await expect(input).toHaveAttribute("type", "text");
});


async function noSidewaysScroll(page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}

async function signInAs(page) {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(process.env.E2E_USER);
  await page.locator('input[name="password"]').fill(process.env.E2E_PASSWORD);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Signed in as / })).toBeVisible();
}

test("a wrong sign-in is shown on the fields and focus goes there", async ({ page }) => {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill("admin");
  await page.locator('input[name="password"]').fill("not the password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  const pass = page.locator("#login-password");
  await expect(pass).toHaveAttribute("aria-invalid", "true");
  await expect(page.locator('input[name="login"]')).toHaveAttribute("aria-invalid", "true");
  await expect(page.locator(".pb-field__error")).toHaveText("Wrong username or password.");
  await expect(pass).toBeFocused();
  await expect(page.locator('input[name="login"]')).toHaveValue("admin"); // kept
  await expect(pass).toHaveAttribute("aria-describedby", "login-password-error");
});

test("sign-in leads with the password form's one primary button", async ({ page }) => {
  await page.goto("/login");
  await expect(page.locator(".pb-btn--primary")).toHaveCount(1);
  await expect(page.locator(".pb-btn--primary")).toHaveText("Sign in");
});

test("an unknown page is a compact result with a way out, on a phone too", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto("/no-such-page");
  const result = page.locator(".pb-result--compact");
  await expect(result).toBeVisible();
  await expect(result.locator(".pb-result__title")).toHaveText("Not found");
  await expect(result.getByRole("link", { name: /^Go to / })).toBeVisible();
  await noSidewaysScroll(page);
});

test("/x names the file, deletes it on one click, and offers the way on", async ({ page, browser }) => {
  test.skip(!process.env.E2E_USER, "needs an account to upload");
  await signInAs(page);
  const up = await page.request.post("/api/v1/upload", {
    multipart: { file: { name: "to-delete.txt", mimeType: "text/plain", buffer: Buffer.from("bye") } },
  });
  expect(up.ok()).toBeTruthy();
  const made = await up.json();
  const ctx = await browser.newContext({ viewport: { width: 390, height: 800 } });
  const guest = await ctx.newPage();
  await guest.goto(made.deletion_url);
  expect(new URL(guest.url()).hash).toBe(""); // the token left the address bar
  await expect(guest.getByRole("heading", { name: "Delete upload" })).toBeVisible();
  await expect(guest.getByText("This will permanently delete the file below")).toBeVisible();
  await expect(guest.locator("[data-x-name]")).toHaveText("to-delete.txt");
  await noSidewaysScroll(guest);
  await guest.getByRole("button", { name: "Delete permanently" }).click();
  const done = guest.locator(".pb-result--compact[data-status=ok]");
  await expect(done.locator(".pb-result__title")).toHaveText("Deleted");
  await expect(done.getByRole("link", { name: /^Go to / })).toBeFocused();
  expect((await guest.request.get(made.url)).status()).toBe(404);
  // The same link again is a result with a way out, not a dead end.
  await guest.goto("about:blank");
  await guest.goto(made.deletion_url);
  await expect(guest.locator(".pb-result--compact[data-status=down]")).toBeVisible();
  await expect(guest.getByRole("link", { name: /^Go to / }).first()).toBeVisible();
  await ctx.close();
});

test("a locked link says what to do, and a wrong password lands on the field", async ({ page, browser }) => {
  test.skip(!process.env.E2E_USER, "needs an account to upload");
  await signInAs(page);
  const up = await page.request.post("/api/v1/upload", {
    multipart: { password: "hunter2-long-enough", file: { name: "locked-doc.txt", mimeType: "text/plain", buffer: Buffer.from("x") } },
  });
  const made = await up.json();
  const ctx = await browser.newContext({ viewport: { width: 390, height: 800 } });
  const guest = await ctx.newPage();
  await guest.goto(made.url);
  await expect(guest.getByRole("heading", { name: "Password required" })).toBeVisible();
  await expect(guest.getByText("Enter the password the sender gave you")).toBeVisible();
  await expect(guest.getByText("locked-doc")).toHaveCount(0); // nothing about the item
  await expect(guest.locator("#unlock-password")).toBeFocused();
  await guest.locator("#unlock-password").fill("wrong one");
  await guest.getByRole("button", { name: "Unlock" }).click();
  await expect(guest.locator("#unlock-password")).toHaveAttribute("aria-invalid", "true");
  await expect(guest.locator(".pb-field__error")).toHaveText("Wrong password.");
  await expect(guest.locator("#unlock-password")).toBeFocused();
  await noSidewaysScroll(guest);
  await ctx.close();
});
