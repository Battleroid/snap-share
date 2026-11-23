import { test, expect } from "@playwright/test";

// The composer's album prompt after a multi-file upload, and gists in the
// Paste tab. The gist endpoint is mocked with page.route: these specs check
// the UI and the request it sends, not the server's gists.
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
  await expect(page.getByRole("tab", { name: "Upload" })).toBeVisible();
}

// Console errors, minus the browser's own line for an HTTP error response
// (some tests answer with one on purpose).
function watchErrors(page) {
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(m.text()); });
  page.on("pageerror", (e) => errors.push(String(e)));
  return errors;
}

// The composer's current options, as readOptions() sends them.
const options = (page) => page.evaluate(() =>
  Object.fromEntries([...new FormData(document.getElementById("composer-options"))].filter(([, v]) => v !== "")));

const tagOf = () => `c${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;

// A 2x1 PNG.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4zwAE/wEHAAH/4iOeWQAAAABJRU5ErkJggg==";

// dropFiles drops PNGs with these names on the window in one drop.
async function dropFiles(page, names) {
  const dt = await page.evaluateHandle(([b64, names]) => {
    const t = new DataTransfer();
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    for (const n of names) t.items.add(new File([bytes], n, { type: "image/png" }));
    return t;
  }, [PNG, names]);
  await page.dispatchEvent("body", "dragenter", { dataTransfer: dt });
  await page.dispatchEvent("body", "drop", { dataTransfer: dt });
}

const uploadRow = (page, name) => page.locator("[data-filelist] .pb-filerow", { hasText: name });

test("two uploaded files offer an album, which opens with both in order", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  const tag = tagOf();
  const names = [`${tag}-a.png`, `${tag}-b.png`];
  await dropFiles(page, names);

  const offers = page.locator("[data-album-offers]");
  const prompt = offers.locator(".pb-notice");
  await expect(prompt).toHaveCount(1);
  await expect(prompt.locator(".pb-notice__title")).toHaveText("2 files uploaded");
  for (const n of names) await expect(uploadRow(page, n).locator(".pb-icon--status-ok")).toBeVisible();
  await prompt.locator('input[name="title"]').fill(`${tag} album`);

  // A failed create says why and keeps the prompt.
  await page.route("**/api/v1/albums", (r) => r.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "Try again later." }) }), { times: 1 });
  await prompt.getByRole("button", { name: "Create album" }).click();
  await expect(offers.locator('.pb-notice[data-status="down"]')).toHaveText(/Try again later\./);
  await expect(prompt.getByRole("button", { name: "Create album" })).toBeEnabled();
  await expect(prompt.locator('input[name="title"]')).toHaveValue(`${tag} album`);

  const want = await options(page);
  const req = page.waitForRequest((r) => r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/albums");
  await prompt.getByRole("button", { name: "Create album" }).click();
  const body = (await req).postDataJSON();
  expect(body.items).toHaveLength(2);
  expect(body).toEqual({ ...want, title: `${tag} album`, items: body.items });

  const ok = offers.locator('.pb-notice[data-status="ok"]');
  await expect(ok.locator(".pb-notice__title")).toHaveText("Album created");
  await expect(offers.locator(".pb-notice")).toHaveCount(1); // the prompt and its error are gone
  await expect(ok.getByRole("button", { name: "Copy link" })).toBeVisible();
  await expect(page.locator("[data-recent] .pb-filerow").first()).toContainText(`${tag} album`);

  await page.goto(await ok.locator("a").getAttribute("href"));
  await expect(page.getByRole("heading", { name: `${tag} album` })).toBeVisible();
  await expect(page.locator("[data-member] .app-tile__name")).toHaveText(names);
  expect(errors).toEqual([]);
});

test("each drop gets its own prompt, failed files are left out, one file gets none", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  const tag = tagOf();
  await page.route("**/api/v1/uploads", (r) => {
    if (r.request().postDataJSON()?.name?.includes("-bad")) {
      return r.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "Not this one." }) });
    }
    return r.continue();
  });
  const prompts = page.locator("[data-album-offers] > *");

  await dropFiles(page, [`${tag}-1.png`, `${tag}-bad.png`, `${tag}-3.png`]);
  await expect(prompts).toHaveCount(1);
  await expect(prompts.first().locator(".pb-notice__title")).toHaveText("2 files uploaded");
  await expect(uploadRow(page, `${tag}-bad.png`).locator(".pb-filerow__meta")).toHaveText("Not this one.");

  await dropFiles(page, [`${tag}-solo.png`]);
  await expect(uploadRow(page, `${tag}-solo.png`).locator(".pb-icon--status-ok")).toBeVisible();
  await expect(prompts).toHaveCount(1);

  await dropFiles(page, [`${tag}-4.png`, `${tag}-5.png`, `${tag}-6.png`]);
  await expect(prompts).toHaveCount(2);
  await expect(prompts.first().locator(".pb-notice__title")).toHaveText("3 files uploaded"); // newest on top
  await prompts.first().getByRole("button", { name: "Dismiss" }).click();
  await expect(prompts).toHaveCount(1);
  await expect(prompts.first().locator(".pb-notice__title")).toHaveText("2 files uploaded");

  await prompts.first().locator('input[name="title"]').fill(`${tag} pair`);
  await prompts.first().getByRole("button", { name: "Create album" }).click();
  const link = prompts.first().locator('.pb-notice[data-status="ok"] a');
  await expect(link).toBeVisible();
  await page.goto(await link.getAttribute("href"));
  await expect(page.locator("[data-member] .app-tile__name")).toHaveText([`${tag}-1.png`, `${tag}-3.png`]);
  expect(errors).toEqual([]);
});

test("adding files to the Paste tab makes a gist", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  await page.getByRole("tab", { name: "Paste" }).click();
  const form = page.locator("[data-paste-form]");
  const blocks = form.locator("[data-snippet]");
  const title = form.locator('input[name="title"]');
  const add = form.getByRole("button", { name: "Add file" });
  const removes = form.getByRole("button", { name: /^Remove file/ });

  // One block: today's paste form.
  await expect(blocks).toHaveCount(1);
  await expect(form.getByRole("button", { name: "Create paste" })).toBeVisible();
  await expect(title).toBeHidden();
  await expect(removes).toHaveCount(0);
  await expect(blocks.first().locator("legend")).toBeHidden();

  await add.click();
  await expect(blocks).toHaveCount(2);
  await expect(blocks.nth(1).locator('input[name="name"]')).toBeFocused();
  await expect(form.getByRole("button", { name: "Create gist" })).toBeVisible();
  await expect(form.getByRole("button", { name: "Create paste" })).toHaveCount(0);
  await expect(title).toBeVisible();
  await expect(removes).toHaveCount(2);
  await expect(blocks.locator("legend")).toHaveText(["file 1", "file 2"]);

  await add.click();
  await expect(blocks).toHaveCount(3);
  const fill = async (i, name, lang, content) => {
    const b = blocks.nth(i);
    await b.locator('input[name="name"]').fill(name);
    await b.locator('input[name="lang"]').fill(lang);
    await b.locator('textarea[name="content"]').fill(content);
  };
  await fill(0, "main.go", "", "package main\n");
  await fill(1, "drop.py", "python", "print(1)\n");
  await fill(2, "", "markdown", "# notes\n");

  // Removing the middle file renumbers the rest.
  await form.getByRole("button", { name: "Remove file 2", exact: true }).click();
  await expect(blocks).toHaveCount(2);
  await expect(blocks.locator("legend")).toHaveText(["file 1", "file 2"]);
  await expect(blocks.nth(0).locator('input[name="name"]')).toHaveValue("main.go");
  await expect(blocks.nth(1).locator('input[name="name"]')).toHaveValue("");
  await expect(blocks.nth(1).locator('input[name="lang"]')).toHaveValue("markdown");
  await title.fill("demo gist");

  // Every block needs content before the form submits.
  await add.click();
  let gists = 0;
  const base = test.info().project.use.baseURL;
  const gist = {
    id: 999999, kind: "gist", slug: "e2egist", url: `${base}/e2egist`, page_url: `${base}/e2egist`, name: "demo gist", title: "demo gist",
    files: [{ name: "main.go", lang: "go", size: 13, raw_url: `${base}/e2egist/raw/main.go`, download_url: `${base}/e2egist/download/main.go` }],
  };
  let answer = { status: 400, body: { error: "The gist was not created." } };
  const sent = [];
  await page.route("**/api/v1/gists", (r) => {
    gists++;
    sent.push({ method: r.request().method(), type: r.request().headers()["content-type"], body: r.request().postDataJSON() });
    return r.fulfill({ status: answer.status, contentType: "application/json", body: JSON.stringify(answer.body) });
  });
  await form.getByRole("button", { name: "Create gist" }).click();
  expect(await form.evaluate((f) => f.checkValidity())).toBe(false);
  await form.getByRole("button", { name: "Remove file 3", exact: true }).click();
  await expect(blocks).toHaveCount(2);
  expect(gists).toBe(0);

  // An error keeps every block.
  await form.getByRole("button", { name: "Create gist" }).click();
  await expect(form.locator('[data-result] .pb-notice[data-status="down"]')).toHaveText(/The gist was not created\./);
  await expect(blocks).toHaveCount(2);
  await expect(title).toHaveValue("demo gist");

  answer = { status: 201, body: gist };
  await form.getByRole("button", { name: "Create gist" }).click();
  const ok = form.locator('[data-result] .pb-notice[data-status="ok"]');
  await expect(ok.locator(".pb-notice__title")).toHaveText("Gist created");
  await expect(ok.locator("a")).toHaveAttribute("href", gist.url);
  expect(gists).toBe(2);
  const want = await options(page);
  for (const s of sent) {
    expect(s.method).toBe("POST");
    expect(s.type).toContain("application/json");
    expect(s.body).toEqual({
      ...want,
      title: "demo gist",
      files: [
        { name: "main.go", lang: "", content: "package main\n" },
        { name: "", lang: "markdown", content: "# notes\n" },
      ],
    });
  }
  await expect(page.locator("[data-recent] .pb-filerow").first()).toContainText("demo gist");

  // Back to one empty block, which makes a paste again.
  await expect(blocks).toHaveCount(1);
  await expect(blocks.first().locator('textarea[name="content"]')).toHaveValue("");
  await expect(blocks.first().locator('input[name="name"]')).toHaveValue("");
  await expect(title).toBeHidden();
  await expect(title).toHaveValue("");
  await expect(removes).toHaveCount(0);
  await blocks.first().locator('textarea[name="content"]').fill("hello\n");
  const req = page.waitForRequest((r) => r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/pastes");
  await form.getByRole("button", { name: "Create paste" }).click();
  expect((await req).postDataJSON()).toEqual({ ...want, content: "hello\n" });
  await expect(ok.locator(".pb-notice__title")).toHaveText("Paste created");
  expect(gists).toBe(2);
  expect(errors).toEqual([]);
});

test("the Paste tab holds at most 50 files", async ({ page }) => {
  await signIn(page);
  await page.getByRole("tab", { name: "Paste" }).click();
  const form = page.locator("[data-paste-form]");
  const blocks = form.locator("[data-snippet]");
  const add = form.getByRole("button", { name: "Add file" });
  for (let i = 1; i < 50; i++) await add.click();
  await expect(blocks).toHaveCount(50);
  await expect(add).toBeDisabled();
  await form.getByRole("button", { name: "Remove file 50", exact: true }).click();
  await expect(blocks).toHaveCount(49);
  await expect(add).toBeEnabled();
});
