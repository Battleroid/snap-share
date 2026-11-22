// Visual review: signs in, creates sample content, and screenshots signed-in
// pages at desktop and phone widths into out/review/.
//   BASE_URL=https://host E2E_USER=u E2E_PASSWORD=p node review.mjs [/extra/path ...]
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";

const base = process.env.BASE_URL;
const user = process.env.E2E_USER;
const password = process.env.E2E_PASSWORD;
if (!base || !user || !password) {
  console.error("set BASE_URL, E2E_USER and E2E_PASSWORD");
  process.exit(2);
}
mkdirSync("out/review", { recursive: true });

const browser = await chromium.launch();
const ctx = await browser.newContext({
  ignoreHTTPSErrors: true,
  viewport: { width: 1280, height: 900 },
  userAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36",
});
const page = await ctx.newPage();
const problems = [];
page.on("console", (m) => { if (m.type() === "error") problems.push(`${page.url()}: ${m.text()}`); });
page.on("pageerror", (e) => problems.push(`${page.url()}: ${e}`));

await page.goto(base + "/login");
await page.fill('input[name="login"]', user);
await page.fill('input[name="password"]', password);
await page.click('button[type="submit"]');
await page.waitForURL((u) => !u.pathname.startsWith("/login"));

// Sample content through the API, using the session (same origin).
const created = await page.evaluate(async () => {
  const canvas = document.createElement("canvas");
  canvas.width = 1200; canvas.height = 750;
  const g = canvas.getContext("2d");
  const grad = g.createLinearGradient(0, 0, 1200, 750);
  grad.addColorStop(0, "#1f3fd6"); grad.addColorStop(1, "#45d6a5");
  g.fillStyle = grad; g.fillRect(0, 0, 1200, 750);
  g.fillStyle = "#ffd23f"; g.beginPath(); g.arc(860, 300, 160, 0, Math.PI * 2); g.fill();
  const png = await new Promise((r) => canvas.toBlob(r, "image/png"));
  const fd = new FormData();
  fd.append("file", png, "sunrise-over-rack.png");
  const up = await (await fetch("/api/v1/upload", { method: "POST", body: fd })).json();
  const paste = await (await fetch("/api/v1/pastes", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "main.go", content: 'package main\n\nimport "fmt"\n\n// main greets.\nfunc main() {\n\tfor i := 0; i < 3; i++ {\n\t\tfmt.Println("hello", i)\n\t}\n}\n' }),
  })).json();
  const md = await (await fetch("/api/v1/pastes", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "notes.md", content: "# Rack notes\n\n- Swap the **UPS** battery\n- Re-seat `sank` NVMe\n\n| host | port |\n|---|---|\n| nas-01 | 5001 |\n" }),
  })).json();
  return [up, paste, md].map((x) => new URL(x.url).pathname);
});

const paths = ["/", "/dashboard", "/settings/profile", "/settings/security", "/settings/defaults", "/admin/settings", ...created, ...process.argv.slice(2)];
for (const width of [1280, 390]) {
  await page.setViewportSize({ width, height: 900 });
  for (const p of paths) {
    await page.goto(base + p, { waitUntil: "networkidle" });
    const name = (p.replace(/[^a-z0-9]+/gi, "_").replace(/^_|_$/g, "") || "home") + (width < 600 ? ".mobile" : "");
    await page.screenshot({ path: `out/review/${name}.png`, fullPage: true });
    console.log(`out/review/${name}.png`);
  }
}
if (problems.length) console.log("console problems:\n  " + [...new Set(problems)].join("\n  "));
await browser.close();
