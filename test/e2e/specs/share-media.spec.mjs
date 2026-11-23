import { test, expect } from "@playwright/test";
import { deflateSync } from "node:zlib";
import { fileURLToPath } from "node:url";

// Share page media. Images fill their frame: a smaller image toggles to its
// actual size on click, a larger one keeps the link to the full-size file.
// Videos take their own aspect ratio, so the frame shows no bars.
//
// Needs a verified account: E2E_USER and E2E_PASSWORD sign in through /login,
// or COOKIE="name=value" supplies a session directly.
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
const cookie = process.env.COOKIE;
test.skip(!(user && password) && !cookie, "set E2E_USER and E2E_PASSWORD (or COOKIE) for a verified account");

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
}

// png is a solid grey RGB PNG of w×h.
function png(w, h) {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (buf) => {
    let c = 0xffffffff;
    for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const td = Buffer.concat([Buffer.from(type), data]);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc(td));
    return Buffer.concat([len, td, sum]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // RGB
  const row = Buffer.alloc(1 + w * 3, 0x80);
  row[0] = 0; // filter: none
  const raw = Buffer.concat(Array.from({ length: h }, () => row));
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
}

const CLIP = fileURLToPath(new URL("../fixtures/clip.mp4", import.meta.url)); // 320×240

async function upload(page, name, w, h) {
  const file = w ? { name, mimeType: "image/png", buffer: png(w, h) } : name;
  await page.locator("[data-file-input]").setInputFiles(file);
  if (!w) name = "clip.mp4";
  const link = page.locator("[data-filelist] .pb-filerow", { hasText: name }).locator("a").first();
  await expect(link).toBeVisible();
  return link.getAttribute("href");
}

test("a small image fills its frame and toggles to its actual size", async ({ page }) => {
  await signIn(page);
  const url = await upload(page, `small-${Date.now()}.png`, 80, 45);
  await page.goto(url);
  const frame = page.locator("a[data-zoom]");
  const img = frame.locator("img");
  await expect(frame).toHaveAttribute("data-zoomable", "");
  await expect(frame).toHaveAttribute("aria-pressed", "false");
  const fit = (await img.evaluate((i) => getComputedStyle(i).objectFit));
  expect(fit).toBe("contain");

  await frame.click();
  await expect(frame).toHaveAttribute("aria-pressed", "true");
  expect(await img.evaluate((i) => getComputedStyle(i).objectFit)).toBe("scale-down");
  expect(page.url()).toBe(url); // stayed on the page

  await frame.press(" ");
  await expect(frame).toHaveAttribute("aria-pressed", "false");
  expect(await img.evaluate((i) => getComputedStyle(i).objectFit)).toBe("contain");
});

test("a large image keeps the link to the full-size file", async ({ page }) => {
  await signIn(page);
  const url = await upload(page, `large-${Date.now()}.png`, 3000, 1700);
  await page.goto(url);
  const frame = page.locator("a[data-zoom]");
  await expect(frame.locator("img")).toHaveJSProperty("complete", true);
  await expect(frame).not.toHaveAttribute("data-zoomable", "");
  await expect(frame).toHaveAttribute("aria-label", "Open full size");
  const href = await frame.getAttribute("href");
  await frame.click();
  await expect(page).toHaveURL(href);
});

test("a video's frame follows the video's aspect ratio, without bars", async ({ page }) => {
  // Tall enough that the 4:3 clip at full width stays under the 80vh cap.
  await page.setViewportSize({ width: 1280, height: 1400 });
  await signIn(page);
  const url = await upload(page, CLIP);
  await page.goto(url);
  const frame = page.locator(".app-share-video");
  const video = frame.locator("video");
  await video.evaluate((v) => { v.preload = "metadata"; v.load(); });
  await expect.poll(() => video.evaluate((v) => v.readyState)).toBeGreaterThanOrEqual(1);
  const g = await video.evaluate((v) => {
    const r = v.getBoundingClientRect();
    const f = v.parentElement.getBoundingClientRect();
    return { w: r.width, h: r.height, vw: v.videoWidth, vh: v.videoHeight, fh: f.height };
  });
  expect(g.vw / g.vh).toBeCloseTo(4 / 3, 2);
  expect(Math.abs(g.w / g.h - g.vw / g.vh)).toBeLessThan(0.02); // the box is the video's shape
  expect(g.fh - g.h).toBeLessThanOrEqual(1.5); // the frame wraps it (its bottom border aside)
});
