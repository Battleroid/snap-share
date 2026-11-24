import { test, expect, request } from "@playwright/test";
import { fileURLToPath } from "node:url";

// Needs a verified account: E2E_USER and E2E_PASSWORD sign in through /login,
// or COOKIE="name=value" supplies a session directly.
// The composer's Recent list holds the newest 8 items, so these specs use an
// account of their own when one is given (make e2e-isolated creates it):
// uploads by the other specs running in parallel would push the clip out of
// the list before its teaser is made.
const user = process.env.E2E_MEDIA_USER || process.env.E2E_USER;
const password = process.env.E2E_MEDIA_USER ? process.env.E2E_MEDIA_PASSWORD : process.env.E2E_PASSWORD;
const cookie = process.env.COOKIE;
test.skip(!(user && password) && !cookie, "set E2E_USER and E2E_PASSWORD (or COOKIE) for a verified account");

// The stats classifier drops HeadlessChrome; look like a desktop browser.
test.use({ userAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/159.0.0.0 Safari/537.36" });

// A 4 s 320x240 H.264 clip (ffmpeg testsrc2), about 75 KB.
const CLIP = fileURLToPath(new URL("../fixtures/clip.mp4", import.meta.url));

async function signIn(page) {
  if (cookie) {
    const [name, ...v] = cookie.split("=");
    await page.context().addCookies([{ name, value: v.join("="), url: test.info().project.use.baseURL }]);
  } else {
    await page.goto("/login");
    await page.locator('input[name="login"]').fill(user);
    await page.locator('input[name="password"]').fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
  }
  await page.goto("/");
  await expect(page.getByRole("tab", { name: "Upload" })).toBeVisible();
}

// uploadClip uploads the fixture through the composer and waits until its
// recent-uploads card carries the teaser (the media job has run).
async function uploadClip(page) {
  await page.locator("[data-file-input]").setInputFiles(CLIP);
  const row = page.locator("[data-filelist] .app-result").first();
  await expect(row).toBeVisible({ timeout: 20_000 });
  const href = await row.getByRole("link", { name: "Open" }).getAttribute("href");
  const slug = new URL(href).pathname.slice(1);
  const card = page.locator(`[data-recent] [data-preview="/${slug}/preview"]`);
  await expect.poll(async () => {
    await page.goto("/");
    return card.count();
  }, { timeout: 60_000, intervals: [1_000] }).toBe(1);
  return { slug, card };
}

test("hovering a recent video plays its teaser over the poster", async ({ page }) => {
  test.setTimeout(120_000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  await signIn(page);
  const { slug, card } = await uploadClip(page);

  const poster = card.locator("img");
  await expect(poster).toHaveAttribute("src", `/${slug}/thumb`);
  expect(await poster.evaluate((i) => i.complete && i.naturalWidth)).toBeGreaterThan(0);
  await expect(card.locator(".app-vis")).toContainText("unlisted");

  await page.mouse.move(0, 0);
  await card.hover();
  const video = card.locator("video.app-preview");
  await expect(video).toHaveCount(1);
  await expect(video).toHaveAttribute("data-state", "on", { timeout: 10_000 });
  for (const a of ["muted", "playsinline", "loop"]) await expect(video).toHaveAttribute(a, "");
  await expect.poll(() => video.evaluate((v) => !v.paused && v.currentTime > 0)).toBe(true);
  await expect.poll(() => card.locator(".app-preview-progress").evaluate((p) => p.style.getPropertyValue("--value"))).not.toBe("");
  expect(await page.locator("video.app-preview").count()).toBe(1); // one at a time

  // Leaving removes the teaser and the poster shows again.
  await page.mouse.move(0, 0);
  await expect(video).toHaveCount(0);
  await expect(card.locator(".app-preview-progress")).toHaveCount(0);
  await expect(poster).toBeVisible();

  // The share page embeds the video for chat apps and shows the poster.
  const res = await page.request.get(`/${slug}`, { headers: { "User-Agent": "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)" } });
  const html = await res.text();
  expect(html).toContain('<meta property="og:video:type" content="video/mp4">');
  expect(html).toContain(`<meta property="og:image" content="${new URL(`/${slug}/thumb`, page.url())}">`);
  await page.goto(`/${slug}`);
  await expect(page.locator("figure.pb-player[data-player] video")).toHaveAttribute("poster", new RegExp(`/${slug}/thumb$`));
  await expect(page.locator("video[controls]")).toHaveCount(0); // the kit MediaPlayer, not native controls
  await expect(page.locator(".app-share-meta dl")).toContainText("320×240 · 0:04");
  expect(errors).toEqual([]);
});

test("the teaser plays under reduced motion, without the fade", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const { card } = await uploadClip(page);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.mouse.move(0, 0);
  await card.hover();
  const video = card.locator("video.app-preview");
  await expect(video).toHaveAttribute("data-state", "on", { timeout: 10_000 });
  expect(await video.evaluate((v) => getComputedStyle(v).transitionDuration)).toBe("0s");
});

test("a new video says processing until its poster is made", async ({ page }) => {
  test.setTimeout(120_000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  await signIn(page);
  await page.locator("[data-file-input]").setInputFiles(CLIP);
  const row = page.locator("[data-filelist] .app-result").first();
  await expect(row).toBeVisible({ timeout: 20_000 });
  const slug = new URL(await row.getByRole("link", { name: "Open" }).getAttribute("href")).pathname.slice(1);

  // The upload response says the preview is pending, and the first poll is
  // 5 s away, so the marker is there whatever the job's speed.
  const marker = row.locator(".app-proc");
  await expect(marker).toContainText("processing");
  await expect(marker).toHaveAttribute("hx-get", /\/dashboard\/items\/\d+\/media\?as=lead$/);
  await expect(marker.locator(".pb-skel")).toBeVisible();

  // The poll swaps the poster in, in the same row, without a reload.
  const poster = row.locator(".pb-item__lead img");
  await expect(poster).toHaveAttribute("src", new RegExp(`/${slug}/thumb`), { timeout: 60_000 });
  expect(await poster.evaluate((i) => i.complete && i.naturalWidth)).toBeGreaterThan(0);
  await expect(row.locator(".app-proc")).toHaveCount(0);

  // A visitor never sees the marker or the item's state.
  const visitor = await request.newContext({ baseURL: test.info().project.use.baseURL, ignoreHTTPSErrors: true });
  try {
    const html = await (await visitor.get(`/${slug}`)).text();
    expect(html).not.toContain("processing");
    expect(html).not.toContain("/media?as=");
  } finally {
    await visitor.dispose();
  }
  expect(errors).toEqual([]);
});
