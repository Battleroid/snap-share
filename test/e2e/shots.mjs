// Full-page screenshots for visual review: node shots.mjs path1 path2 ...
// Writes out/shots/<name>.png. Set BASE_URL; optional COOKIE="name=value" to sign in.
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const base = process.env.BASE_URL;
const paths = process.argv.slice(2);
if (!base || paths.length === 0) {
  console.error("usage: BASE_URL=https://host node shots.mjs /path ...");
  process.exit(2);
}
mkdirSync("out/shots", { recursive: true });
const browser = await chromium.launch();
for (const width of [1280, 390]) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width, height: 900 } });
  if (process.env.COOKIE) {
    const [name, ...v] = process.env.COOKIE.split("=");
    await ctx.addCookies([{ name, value: v.join("="), url: base }]);
  }
  const page = await ctx.newPage();
  const problems = [];
  page.on("console", (m) => { if (m.type() === "error" || m.type() === "warning") problems.push(m.text()); });
  page.on("pageerror", (e) => problems.push(String(e)));
  for (const p of paths) {
    await page.goto(base + p, { waitUntil: "networkidle" });
    const name = (p.replace(/[^a-z0-9]+/gi, "_").replace(/^_|_$/g, "") || "root") + (width < 600 ? ".mobile" : "");
    await page.screenshot({ path: `out/shots/${name}.png`, fullPage: true });
    console.log(`out/shots/${name}.png`);
  }
  if (problems.length) console.log("console problems:\n  " + problems.join("\n  "));
  await ctx.close();
}
await browser.close();
