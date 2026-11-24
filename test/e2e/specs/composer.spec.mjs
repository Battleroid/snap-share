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

// A file is a queue row while it uploads (or after it failed) and a result row once it is done.
const uploadRow = (page, name) => page.locator("[data-filelist] .pb-upq__item, [data-filelist] .app-result", { hasText: name });
const doneRow = (page, name) => page.locator("[data-filelist] .app-result", { hasText: name });

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
  for (const n of names) await expect(doneRow(page, n)).toBeVisible();
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
  await expect(page.locator("[data-recent] .app-recent").first()).toContainText(`${tag} album`);

  await page.goto(await ok.locator("a").getAttribute("href"));
  await expect(page.getByRole("heading", { name: `${tag} album` })).toBeVisible();
  await expect(page.locator("[data-member] .pb-media__name")).toHaveText(names);
  expect(errors).toEqual([]);
});

test("one album prompt counts every uploaded file, failed files are left out, one file gets none", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  const tag = tagOf();
  await page.route("**/api/v1/uploads", (r) => {
    if (r.request().method() === "POST" && r.request().postDataJSON()?.name?.includes("-bad")) {
      return r.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "Not this one." }) });
    }
    return r.continue();
  });
  const offers = page.locator("[data-album-offers]");
  const prompt = offers.locator(".pb-notice");

  await dropFiles(page, [`${tag}-1.png`]);
  await expect(doneRow(page, `${tag}-1.png`)).toBeVisible();
  await expect(prompt).toHaveCount(0); // one file gets none

  await dropFiles(page, [`${tag}-2.png`, `${tag}-bad.png`, `${tag}-3.png`]);
  await expect(prompt).toHaveCount(1);
  await expect(prompt.locator(".pb-notice__title")).toHaveText("3 files uploaded"); // the first one counts too
  await expect(page.locator("[data-filelist] .pb-upq__item[data-state=failed]", { hasText: `${tag}-bad.png` }).locator(".pb-upq__txt")).toHaveText("Not this one.");

  // More uploads change the one prompt's count; they never stack another.
  await dropFiles(page, [`${tag}-4.png`, `${tag}-5.png`]);
  await expect(prompt).toHaveCount(1);
  await expect(prompt.locator(".pb-notice__title")).toHaveText("5 files uploaded");

  // Dismissing forgets them: the next two start a new count.
  await prompt.getByRole("button", { name: "Dismiss" }).click();
  await expect(prompt).toHaveCount(0);
  await dropFiles(page, [`${tag}-6.png`, `${tag}-7.png`]);
  await expect(prompt).toHaveCount(1);
  await expect(prompt.locator(".pb-notice__title")).toHaveText("2 files uploaded");

  await prompt.locator('input[name="title"]').fill(`${tag} pair`);
  await prompt.getByRole("button", { name: "Create album" }).click();
  const link = offers.locator('.pb-notice[data-status="ok"] a');
  await expect(link).toBeVisible();
  await page.goto(await link.getAttribute("href"));
  await expect(page.locator("[data-member] .pb-media__name")).toHaveText([`${tag}-6.png`, `${tag}-7.png`]);
  expect(errors).toEqual([]);
});

test("a failed upload says why and retries, a running one cancels, and leaving mid-transfer asks first", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  const tag = tagOf();
  let flaky = 0;
  await page.route("**/api/v1/uploads", (r) => {
    const name = r.request().method() === "POST" ? r.request().postDataJSON()?.name || "" : "";
    if (name.includes("-flaky") && ++flaky === 1) {
      return r.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "Not this time." }) });
    }
    if (name.includes("-hang")) return; // never answered: the file stays in flight
    return r.continue();
  });
  const rowOf = (n) => page.locator("[data-filelist] .pb-upq__item", { hasText: n });
  const leaves = () => page.evaluate(() => {
    const e = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(e);
    return e.defaultPrevented;
  });

  // A failure says why; Retry sends it again and it lands as a result row.
  await dropFiles(page, [`${tag}-flaky.png`]);
  const failed = rowOf(`${tag}-flaky.png`);
  await expect(failed).toHaveAttribute("data-state", "failed");
  await expect(failed.locator(".pb-upq__txt")).toHaveText("Not this time.");
  expect(await leaves()).toBe(false); // nothing is in flight
  await failed.getByRole("button", { name: `Retry ${tag}-flaky.png` }).click();
  await expect(doneRow(page, `${tag}-flaky.png`)).toBeVisible();
  await expect(failed).toHaveCount(0);
  expect(flaky).toBe(2);

  // A file in flight blocks leaving, shows in the aggregate line, and cancels.
  await dropFiles(page, [`${tag}-hang.png`]);
  const hung = rowOf(`${tag}-hang.png`);
  await expect(hung).toHaveAttribute("data-state", "uploading");
  await expect(page.locator("[data-upq] .pb-upq__title")).toContainText("Uploading 1 of 1");
  expect(await leaves()).toBe(true);
  await page.getByRole("button", { name: "Cancel all" }).click();
  await expect(hung).toHaveAttribute("data-state", "canceled");
  expect(await leaves()).toBe(false);
  await hung.getByRole("button", { name: `Remove ${tag}-hang.png` }).click();
  await expect(hung).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("paste and link errors sit on the field they are about", async ({ page }) => {
  await signIn(page);
  let sent = 0;
  await page.route("**/api/v1/pastes", (r) => { sent++; return r.continue(); });
  await page.getByRole("tab", { name: "Paste" }).click();
  const paste = page.locator("[data-paste-form]");
  const content = paste.locator('textarea[name="content"]');
  await paste.getByRole("button", { name: "Create paste" }).click();
  await expect(content).toHaveAttribute("aria-invalid", "true");
  await expect(content).toBeFocused();
  await expect(paste.locator(".pb-field__error")).toHaveText("Enter something to paste.");
  expect(sent).toBe(0);
  await content.fill("x");
  await expect(content).not.toHaveAttribute("aria-invalid", "true");
  await expect(paste.locator(".pb-field__error")).toHaveCount(0);

  await page.getByRole("tab", { name: "Link" }).click();
  const link = page.locator("[data-link-form]");
  const url = link.locator('input[name="url"]');
  const slug = link.locator('input[name="slug"]');
  await link.getByRole("button", { name: "Shorten" }).click();
  await expect(url).toHaveAttribute("aria-invalid", "true");
  await expect(url).toBeFocused();

  let answer = { status: 409, body: { error: "That name is taken." } };
  await page.route("**/api/v1/links", (r) => r.fulfill({ status: answer.status, contentType: "application/json", body: JSON.stringify(answer.body) }));
  await url.fill("https://example.com/a");
  await slug.fill("taken-name");
  await link.getByRole("button", { name: "Shorten" }).click();
  await expect(slug).toHaveAttribute("aria-invalid", "true");
  await expect(slug).toBeFocused();
  await expect(slug.locator("xpath=ancestor::div[contains(@class,'pb-field')][1]").locator(".pb-field__error")).toHaveText("That name is taken.");
  await expect(url).not.toHaveAttribute("aria-invalid", "true");
  await expect(link.locator("[data-result] .pb-notice")).toHaveCount(0);
  await slug.fill("another-name");
  await expect(slug).not.toHaveAttribute("aria-invalid", "true");

  // An error that is about neither field is one notice under the form.
  answer = { status: 500, body: { error: "Try again later." } };
  await link.getByRole("button", { name: "Shorten" }).click();
  await expect(link.locator('[data-result] .pb-notice[data-status="down"]')).toHaveText(/Try again later./);
  await expect(url).not.toHaveAttribute("aria-invalid", "true");
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
  const ok = form.locator("[data-result]");
  await expect(ok.getByRole("status")).toHaveText("Gist created");
  await expect(ok.locator(".app-result a").first()).toHaveAttribute("href", gist.url);
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
  await expect(page.locator("[data-recent] .app-recent").first()).toContainText("demo gist");

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
  await expect(ok.getByRole("status")).toHaveText("Paste created");
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

// The kit's upload settings (.pb-dropset): the summary comes from the controls.
const summary = (page) => page.locator("[data-dropset-summary]");
const openSettings = async (page) => {
  const bar = page.locator(".pb-dropset__bar");
  if ((await bar.getAttribute("aria-expanded")) !== "true") await bar.click();
  await expect(page.locator("#composer-opts")).toBeVisible();
};
// The expiry choices read in words (web.ExpiryChoices); the form values stay 1h, 7d, ...
const words = { never: "never", "1h": "1 hour", "1d": "1 day", "7d": "7 days", "30d": "30 days", "90d": "90 days" };
const seg = (page, group, text) => page.locator(`#composer-options [aria-label="${group}"] label`, { hasText: new RegExp(`^${text}$`) });

test("upload settings: the summary follows the controls, and the options reach the upload", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  await expect(page.locator("#composer-opts")).toBeHidden(); // closed until someone changes something
  await expect(summary(page)).toHaveText(/^(never expires|expires \w+) · \w+ · (random|word|original) name · no password$/);

  await openSettings(page);
  await seg(page, "Expires", words["7d"]).click();
  await seg(page, "File names", "words").click();
  await page.locator('#composer-options select[name="visibility"]').selectOption("private");
  await expect(page.locator("#composer-pw")).toBeHidden();
  await page.getByRole("checkbox", { name: "Password", exact: true }).check();
  await expect(page.locator("#composer-pw")).toBeVisible();
  await page.getByLabel("Share password").fill("hunter2-hunter2");
  await expect(summary(page)).toHaveText("expires 7 days · private · word name · password");
  expect(await summary(page).textContent()).not.toContain("hunter2");

  const sent = [];
  await page.route("**/api/v1/uploads", (r) => {
    sent.push(r.request().postDataJSON());
    return r.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "Stopped here." }) });
  });
  const tag = tagOf();
  await dropFiles(page, [`${tag}.png`]);
  await expect(uploadRow(page, `${tag}.png`)).toBeVisible();
  expect(sent).toHaveLength(1);
  expect(sent[0].options).toEqual({ expires: "7d", visibility: "private", naming: "words", password: "hunter2-hunter2" });

  // With the switch off the field is not sent (and is emptied); Reset puts back the saved defaults.
  await page.getByRole("checkbox", { name: "Password", exact: true }).uncheck();
  await expect(page.locator("#composer-pw")).toBeHidden();
  expect(await options(page)).not.toHaveProperty("password");
  await expect(summary(page)).toHaveText("expires 7 days · private · word name · no password");
  await page.getByRole("button", { name: "Reset", exact: true }).click();
  await expect(summary(page)).toHaveText(/ · no password$/);
  expect(errors).toEqual([]);
});

test("upload settings: Save as default updates Settings > Defaults and keeps the rest", async ({ page }) => {
  const errors = watchErrors(page);
  await signIn(page);
  const read = async () => {
    await page.goto("/settings/defaults");
    return page.evaluate(() => {
      const f = document.querySelector('form[action="/settings/defaults"]');
      const v = (n) => f.elements[n];
      return {
        naming: f.querySelector('input[name="naming"]:checked').value, visibility: v("visibility").value, expiry: v("expiry").value,
        random_length: v("random_length").value, strip_location: v("strip_location").checked, direct_link: v("direct_link").checked,
        embed_title: v("embed_title").value, embed_color: v("embed_color").value,
      };
    });
  };
  const before = await read();
  try {
    await page.goto("/");
    await openSettings(page);
    const expiry = before.expiry === "30d" ? "1d" : "30d";
    const naming = before.naming === "words" ? "original" : "words";
    const visibility = before.visibility === "private" ? "unlisted" : "private";
    await seg(page, "Expires", words[expiry]).click();
    await seg(page, "File names", naming).click();
    await page.locator('#composer-options select[name="visibility"]').selectOption(visibility);
    const saved = page.waitForResponse((r) => new URL(r.url()).pathname === "/settings/defaults" && r.request().method() === "POST");
    await page.getByRole("button", { name: "Save as default" }).click();
    const res = await saved;
    expect([res.status(), res.url()]).toEqual([303, expect.stringContaining("/settings/defaults")]); // the handler redirects to ?saved=1
    await expect(page.getByRole("button", { name: "Saved" })).toBeVisible();

    expect(await read()).toEqual({ ...before, expiry, naming, visibility });
    // A fresh composer opens on the new defaults.
    await page.goto("/");
    await expect(summary(page)).toContainText(expiry === "never" ? "never expires" : `expires ${words[expiry]}`);
  } finally {
    await page.request.post("/settings/defaults", { form: {
      naming: before.naming, visibility: before.visibility, expiry: before.expiry, random_length: before.random_length,
      ...(before.strip_location ? { strip_location: "1" } : {}), ...(before.direct_link ? { direct_link: "1" } : {}),
      embed_title: before.embed_title, embed_color: before.embed_color,
    } });
  }
  expect(errors).toEqual([]);
});
