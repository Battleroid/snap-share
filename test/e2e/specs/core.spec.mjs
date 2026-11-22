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
