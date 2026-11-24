import { test, expect } from "@playwright/test";

// THROWAWAY: screenshots for the polish PR, deleted before the last commit.
const PHASE = "after";
const ADMIN = { login: "admin", password: "e2e-admin-password" };
const MAILPIT = process.env.MAILPIT_URL || "http://mailpit:8025";
const UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";
const OUT = "out/polish";
test.describe.configure({ mode: "serial" });
test.use({ actionTimeout: 8000 });
test.setTimeout(240_000);

const diag = [];
const shot = async (page, name, w, opts = {}) => {
  await page.setViewportSize({ width: w, height: opts.h || 900 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${OUT}/${PHASE}-${name}-${w}.png`, fullPage: !!opts.full });
};
const step = async (name, fn) => {
  try { await fn(); } catch (e) { diag.push(`${name}: ${String(e).split("\n")[0]}`); }
};

async function signIn(page, login, password) {
  await page.goto("/login");
  await page.locator('input[name="login"]').fill(login);
  await page.locator('input[name="password"]').fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Signed in as / })).toContainText(login);
}

test("shots", async ({ browser, request }) => {
  const ctx = await browser.newContext({ baseURL: process.env.BASE_URL, ignoreHTTPSErrors: true, userAgent: UA, viewport: { width: 1280, height: 900 }, reducedMotion: "reduce" });
  const page = await ctx.newPage();
  page.setDefaultTimeout(8000);
  await signIn(page, ADMIN.login, ADMIN.password);

  // Secret: ready card, burned, passphrase result
  let link = "";
  await step("secret-create", async () => {
    await page.goto("/new/secret");
    await page.locator("#sc-text").fill("the eagle lands at noon");
    await page.locator('select[name="views"]').selectOption("1");
    await page.getByRole("button", { name: "Generate passphrase" }).click();
    await page.getByRole("button", { name: "Create secret" }).click();
    await expect(page.locator("#secret-link")).toBeVisible();
    link = await page.locator("#secret-link").inputValue();
    for (const w of [390, 1280]) await shot(page, "secret-result", w, { full: true });
    await page.getByRole("button", { name: "Create another" }).click();
  });
  const anon = await browser.newContext({ baseURL: process.env.BASE_URL, ignoreHTTPSErrors: true, userAgent: UA, viewport: { width: 390, height: 844 }, reducedMotion: "reduce" });
  const ap = await anon.newPage();
  ap.setDefaultTimeout(8000);
  await step("secret-ready", async () => {
    await ap.goto(link);
    await expect(ap.getByRole("button", { name: "Reveal" })).toBeEnabled();
    for (const w of [390, 1280]) await shot(ap, "secret-ready", w);
    await ap.getByRole("button", { name: "Reveal" }).click();
    await expect(ap.locator("#secret-text")).toBeVisible();
    await ap.goto("about:blank");
    await ap.goto(link);
    await expect(ap.locator("[data-problem]")).toBeVisible();
    for (const w of [390, 1280]) await shot(ap, "secret-burned", w);
  });
  await step("dead", async () => {
    await ap.goto("/nope-nothing-here");
    for (const w of [390, 1280]) await shot(ap, "dead-link", w);
  });

  // Paste with long lines at 390
  await step("paste", async () => {
    const long = "const veryLongIdentifierName = someFunction(argumentNumberOne, argumentNumberTwo, argumentNumberThree, argumentNumberFour);\n".repeat(6);
    const res = await page.request.post("/api/v1/pastes", { data: { content: long, name: "long.js" }, headers: { Accept: "application/json" } });
    const j = await res.json();
    await ap.goto(j.url || j.page_url);
    for (const w of [390, 1280]) await shot(ap, "paste", w);
    const cue = await ap.evaluate(() => {
      const pre = document.querySelector("pre.pb-code__hl, .pb-code__edit, .pb-code__body");
      const out = [];
      for (const el of document.querySelectorAll(".pb-code *")) {
        const cs = getComputedStyle(el);
        if (el.scrollWidth > el.clientWidth + 1 && cs.overflowX !== "visible") out.push({ cls: el.className, more: el.hasAttribute("data-more-end"), mask: cs.maskImage || cs.webkitMaskImage });
      }
      return out;
    });
    diag.push("paste scroll regions: " + JSON.stringify(cue));
  });

  // Composer tabs at 390, with the signed-in admin
  await step("composer", async () => {
    await page.goto("/");
    await shot(page, "composer", 390);
    await shot(page, "composer", 1280);
    const tabs = await page.evaluate(() => { const t = document.querySelector(".pb-tabs"); return { sw: t.scrollWidth, cw: t.clientWidth, more: t.hasAttribute("data-more-end"), cls: t.className, mask: getComputedStyle(t).maskImage }; });
    diag.push("tabs at 900/390: " + JSON.stringify(tabs));
    await page.setViewportSize({ width: 390, height: 844 });
    const t2 = await page.evaluate(() => { const t = document.querySelector(".pb-tabs"); return { sw: t.scrollWidth, cw: t.clientWidth, more: t.hasAttribute("data-more-end"), cls: t.className, mask: getComputedStyle(t).maskImage }; });
    diag.push("tabs at 390: " + JSON.stringify(t2));
  });

  // Keys new
  await step("keys", async () => {
    await page.goto("/settings/keys/new");
    for (const w of [390, 1280]) await shot(page, "keys-new", w, { full: true });
  });

  // Integrations toast
  await step("sharex", async () => {
    await page.goto("/settings/integrations");
    await page.setViewportSize({ width: 1280, height: 900 });
    await Promise.all([page.waitForEvent("download").catch(() => null), page.locator("form button", { hasText: /Download|\.sxcu|uploader/i }).first().click()]);
    await page.waitForTimeout(600);
    await shot(page, "sharex-toast", 1280);
  });

  // Dashboard selection, delete confirm
  await step("dashboard", async () => {
    const tag = `shot${Date.now().toString(36)}`;
    for (const n of ["alpha", "bravo", "charlie", "delta", "echo"]) {
      await page.request.put(`/api/v1/upload/${tag}-${n}.txt`, { data: Buffer.from(n), headers: { Accept: "application/json" } });
    }
    await page.goto(`/dashboard?q=${tag}`);
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.locator("[data-all]").check();
    await page.getByRole("button", { name: "Actions", exact: true }).click();
    await page.getByRole("menuitem", { name: "Set visibility" }).click();
    await page.locator("#dash-visibility select").selectOption("private");
    await page.locator("#dash-visibility").getByRole("button", { name: "Set visibility" }).click();
    await page.waitForTimeout(1200);
    await shot(page, "bulk-kept", 1280);
    await page.getByRole("button", { name: "Actions", exact: true }).click();
    await page.getByRole("menuitem", { name: "Delete" }).click();
    await page.waitForTimeout(300);
    await shot(page, "bulk-delete", 1280);
    await page.locator("#dash-delete").getByRole("button", { name: "Cancel" }).click();
    // an inverted range
    await page.goto("/dashboard?from=2030-01-01&to=2020-01-01");
    await shot(page, "inverted-range", 1280);
  });

  // Item page
  await step("itempage", async () => {
    const res = await page.request.put(`/api/v1/upload/itempage-${Date.now()}.txt`, { data: Buffer.from("x"), headers: { Accept: "application/json" } });
    const j = await res.json();
    await page.goto(`/dashboard/items/${j.id}`);
    for (const w of [390, 1280]) await shot(page, "item-page", w);
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.getByRole("button", { name: "Edit" }).click();
    await page.locator("#item-edit").getByRole("button", { name: "Save" }).click();
    await page.waitForTimeout(1000);
    await shot(page, "item-saved", 1280);
  });

  // Audit
  await step("audit", async () => {
    for (const w of [1120, 1280]) {
      await page.goto("/admin/audit");
      await shot(page, "audit", w);
      const over = await page.evaluate(() => [...document.querySelectorAll(".pb-tablescroll")].map((e) => e.scrollWidth - e.clientWidth));
      diag.push(`audit overflow at ${w}: ${JSON.stringify(over)}`);
    }
  });

  // Blocked extensions
  await step("settings", async () => {
    await page.goto("/admin/settings");
    await page.setViewportSize({ width: 1280, height: 900 });
    const field = page.locator('input[name="uploads.blocked_extensions"], [name="uploads.blocked_extensions"]').first();
    const form = field.locator("xpath=ancestor::form");
    await field.fill("exe\nbad ext!!/");
    await form.getByRole("button", { name: /Save/ }).first().click();
    await page.waitForTimeout(1200);
    await field.scrollIntoViewIfNeeded().catch(() => {});
    await shot(page, "settings-blocked-ext", 1280);
  });

  // Welcome and invite mail: invite a friend
  await step("invite", async () => {
    const email = `friend${Date.now().toString(36)}@e2e.invalid`;
    await page.goto("/admin/invites");
    await page.getByRole("button", { name: "New invite" }).first().click();
    const form = page.locator('form[action="/admin/invites"]');
    await form.locator('input[name="email"]').fill(email);
    await form.getByRole("button", { name: "Create invite" }).click();
    await expect(page.getByText(`Invite created and sent to ${email}`)).toBeVisible();
    let mail;
    for (let i = 0; i < 60 && !mail; i++) {
      const res = await request.get(`${MAILPIT}/api/v1/messages`);
      const { messages = [] } = await res.json();
      const hit = messages.find((m) => (m.To || []).some((t) => t.Address === email));
      if (hit) mail = await (await request.get(`${MAILPIT}/api/v1/message/${hit.ID}`)).json();
      else await new Promise((r) => setTimeout(r, 250));
    }
    diag.push(`mail subject: ${mail.Subject}\n${mail.Text}`);
    const m = await ctx.newPage();
    await m.setContent(mail.HTML);
    for (const w of [390, 1280]) await shot(m, "invite-mail", w, { full: true });
    const link = mail.Text.match(/https:\/\/\S+\/invite#t=[A-Za-z0-9_-]{43}/)[0];
    const gctx = await browser.newContext({ baseURL: process.env.BASE_URL, ignoreHTTPSErrors: true, userAgent: UA, viewport: { width: 390, height: 844 }, reducedMotion: "reduce" });
    const g = await gctx.newPage();
    await g.goto(link);
    await g.locator('input[name="username"]').fill(`friend${Date.now().toString(36)}`);
    await g.locator('input[name="password"]').fill("short");
    await g.getByRole("button", { name: "Create account" }).click();
    await g.waitForTimeout(600);
    for (const w of [390, 1280]) await shot(g, "register-password-error", w);
    await g.locator('input[name="password"]').fill("a new member password");
    await g.getByRole("button", { name: "Create account" }).click();
    await expect(g.getByRole("button", { name: /^Signed in as / })).toBeVisible();
    await g.goto("/");
    for (const w of [390, 1280]) await shot(g, "welcome", w);
  });

  console.log("DIAG\n" + diag.join("\n"));
});
