// Secret composer: encrypts a text or file in the browser (envelope v1, see
// e2e.js) and uploads only ciphertext. Flow: create → derive keys → encrypt
// and PUT each chunk → finalize → show the link once.
//
// Every attempt uses a fresh K_link and salt. A failed chunk PUT is retried
// with the very same ciphertext bytes: a chunk is never encrypted twice.

import * as e2e from "./e2e.js";
import { passphrase as generateWords, password as generateChars } from "./passgen.js";

export const UPLOAD_TOKEN_HEADER = "X-Snap-Upload-Token";
export const MAX_ATTEMPTS = 4;

// HTTPError is a non-2xx API answer; message is the server's sentence.
export class HTTPError extends Error {
  constructor(status, message, body = {}) {
    super(message);
    this.name = "HTTPError";
    this.status = status;
    this.body = body;
  }
}

async function readError(res) {
  let body = {};
  try {
    body = await res.json();
  } catch {
    // not JSON
  }
  return new HTTPError(res.status, typeof body.error === "string" ? body.error : `Request failed (${res.status}).`, body);
}

// retryable reports whether a failed request may be sent again unchanged.
export function retryable(err) {
  if (!(err instanceof HTTPError)) return true; // network error
  return err.status === 408 || err.status === 429 || err.status >= 500;
}

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// sendWithRetry calls send() (which must send the same bytes every time)
// until it succeeds, a non-retryable error comes back, or attempts run out.
export async function sendWithRetry(send, { attempts = MAX_ATTEMPTS, delay = (i) => 400 * 2 ** i, sleep = pause } = {}) {
  let last;
  for (let i = 0; i < attempts; i++) {
    try {
      const res = await send();
      if (res.ok) return res;
      last = await readError(res);
    } catch (err) {
      last = err;
    }
    if (!retryable(last) || i === attempts - 1) break;
    await sleep(delay(i));
  }
  throw last;
}

async function api(method, url, body, headers = {}) {
  const res = await sendWithRetry(
    () =>
      fetch(url, {
        method,
        headers: { "Content-Type": "application/json", Accept: "application/json", ...headers },
        body: JSON.stringify(body),
        credentials: "same-origin",
        cache: "no-store",
      }),
    { attempts: method === "POST" && url.endsWith("/api/v1/secrets") ? 1 : MAX_ATTEMPTS },
  );
  return res.json();
}

// prepare turns the composer input into the plaintext stream and metadata.
export function prepare(input, limits) {
  if (input.kind === e2e.KIND_TEXT) {
    const bytes = new TextEncoder().encode(input.text);
    if (bytes.length === 0) throw new HTTPError(0, "Write the secret first.");
    if (e2e.paddedTextLen(bytes.length) > limits.textMax) {
      throw new HTTPError(0, `Text can be at most ${humanBytes(limits.textMax)}.`);
    }
    return {
      blob: new Blob([e2e.padText(bytes)]),
      meta: { kind: e2e.KIND_TEXT, name: "", type: "text/plain; charset=utf-8", size: bytes.length },
    };
  }
  const f = input.file;
  if (!f) throw new HTTPError(0, "Choose a file first.");
  if (f.size > limits.maxBytes) throw new HTTPError(0, `Files can be at most ${humanBytes(limits.maxBytes)}.`);
  return { blob: f, meta: { kind: e2e.KIND_FILE, name: f.name, type: f.type || "application/octet-stream", size: f.size } };
}

// createSecret runs the whole flow and returns the link. onStep(label,
// done, total) reports progress.
export async function createSecret(input, limits, { origin, onStep = () => {} } = {}) {
  const { blob, meta } = prepare(input, limits);
  onStep("Starting…", 0, 1);
  const init = await api("POST", "/api/v1/secrets", {
    kind: meta.kind,
    size: blob.size,
    views: input.views,
    expires_in: input.expiresIn,
    note: input.note || "",
    notify: Boolean(input.notify),
  });
  const id = e2e.decode16(init.id);
  const auth = { [UPLOAD_TOKEN_HEADER]: init.upload_token };

  // Fresh for this attempt; never reused.
  const key = e2e.newLinkKey();
  const salt = e2e.newSalt();
  const pass = input.passphrase ? input.passphrase : null;
  const kdf = pass ? e2e.defaultKdf() : null;
  if (pass) onStep("Deriving key…", 0, 1);
  const keys = await e2e.deriveKeys(key, id, salt, pass, kdf);
  const probe = await e2e.probeKey(key, id);
  const [hProbe, hAuth] = await Promise.all([e2e.hash(probe), e2e.hash(keys.auth)]);
  const metaCt = await e2e.sealMeta(keys.meta, meta);

  const total = init.chunks;
  let n = 0;
  onStep("Encrypting…", 0, total);
  for await (const ct of e2e.encryptBlob(keys.enc, blob)) {
    const url = `/api/v1/secrets/${init.id}/chunks/${n}`;
    // `ct` is kept until the server has it; a retry resends these bytes.
    await sendWithRetry(() =>
      fetch(url, {
        method: "PUT",
        headers: { ...auth, "Content-Type": "application/octet-stream" },
        body: ct,
        credentials: "same-origin",
        cache: "no-store",
      }),
    );
    n++;
    onStep(total > 1 ? `Uploading ${n} of ${total}…` : "Uploading…", n, total);
  }

  await api(
    "POST",
    `/api/v1/secrets/${init.id}/finalize`,
    {
      salt: e2e.encode(salt),
      kdf,
      h_probe: e2e.encode(hProbe),
      h_auth: e2e.encode(hAuth),
      commit: e2e.encode(keys.commit),
      meta_ct: e2e.encode(metaCt),
    },
    auth,
  );
  const link = e2e.formatLink(origin, id, key);
  for (const b of [key, probe, keys.auth, keys.enc, keys.meta, keys.commit]) b.fill(0);
  return link;
}

export function humanBytes(n) {
  if (n < 1000) return `${n} B`;
  const units = "KMGTPE";
  let v = n;
  let i = -1;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}B`;
}

// ---- page ----

function composer(root) {
  const $ = (sel) => root.querySelector(sel);
  const form = $("[data-composer-form]");
  const result = $("[data-composer-result]");
  const errBox = $("[data-composer-error]");
  const submit = $("[data-submit]");
  const submitIcon = $("[data-submit-icon]");
  const submitLabel = $("[data-submit-label]");
  const limits = {
    maxBytes: Number(root.dataset.maxBytes),
    textMax: Number(root.dataset.textMax),
    maxViews: Number(root.dataset.maxViews),
  };
  const textarea = root.querySelector('textarea[name="text"]');
  const fileInput = root.querySelector('input[type="file"]');
  const passInput = root.querySelector('input[name="passphrase"]');
  const viewsSel = root.querySelector('select[name="views"]');
  const viewsCustom = $("[data-views-custom]");
  const drop = $("[data-drop]");
  let file = null;
  let busy = false;

  const showError = (title, body = "") => {
    errBox.querySelector("[data-error-title]").textContent = title;
    const b = errBox.querySelector("[data-error-body]");
    b.textContent = body;
    b.hidden = !body;
    errBox.hidden = false;
  };
  const clearError = () => {
    errBox.hidden = true;
    for (const f of root.querySelectorAll(".pb-field[data-invalid]")) fieldError(f.querySelector("[aria-invalid]"), "");
  };
  // fieldError puts a message on one field (its .pb-field__error,
  // aria-invalid, data-invalid) and focuses it; "" clears it.
  function fieldError(control, msg) {
    const field = control?.closest(".pb-field");
    if (!field) return;
    field.toggleAttribute("data-invalid", Boolean(msg));
    if (msg) control.setAttribute("aria-invalid", "true");
    else control.removeAttribute("aria-invalid");
    field.querySelector("[data-field-error-text]").textContent = msg;
    field.querySelector("[data-field-error]").hidden = !msg;
    if (msg) control.focus();
  }
  // A field's error goes once it is edited.
  root.addEventListener("input", (e) => {
    if (e.target.getAttribute?.("aria-invalid") === "true") fieldError(e.target, "");
  });

  if (!e2e.supported()) {
    showError("Secrets can't be created here.", "Encryption in the browser needs a secure connection: open this page over https.");
    submit.disabled = true;
    return;
  }

  const kind = () => root.querySelector('input[name="kind"]:checked')?.value ?? e2e.KIND_TEXT;
  const syncKind = () => {
    for (const p of root.querySelectorAll("[data-kind-panel]")) p.hidden = p.dataset.kindPanel !== kind();
  };
  root.addEventListener("change", (e) => {
    if (e.target.name === "kind") syncKind();
    if (e.target === viewsSel) viewsCustom.hidden = viewsSel.value !== "custom";
    if (e.target === fileInput) pickFile(fileInput.files);
  });
  syncKind();

  // Drop zone: drag handlers stay on the zone, so they never fight the
  // page's other drop targets.
  if (drop) {
    const title = $("[data-drop-title]");
    const hint = $("[data-drop-hint]");
    const icon = $("[data-drop-icon]");
    const idle = { title: title.textContent, hint: hint.innerHTML };
    let depth = 0;
    const setState = (state, t = idle.title, h = null) => {
      if (state) drop.dataset.state = state;
      else delete drop.dataset.state;
      icon.className = `pb-icon pb-icon--${state === "over" ? "download" : "upload"} pb-drop__icon`;
      title.textContent = t;
      if (h === null) hint.innerHTML = idle.hint; // static markup from the template
      else hint.textContent = h;
    };
    drop.addEventListener("dragenter", (e) => {
      e.preventDefault();
      depth++;
      setState("over", "Release to encrypt this file");
    });
    drop.addEventListener("dragover", (e) => e.preventDefault());
    drop.addEventListener("dragleave", () => {
      if (--depth <= 0) {
        depth = 0;
        setState(null);
      }
    });
    drop.addEventListener("drop", (e) => {
      e.preventDefault();
      depth = 0;
      setState(null);
      pickFile(e.dataTransfer?.files);
    });
    drop.reset = () => setState(null);
    drop.reject = (msg) => setState("error", idle.title, msg);
  }

  function pickFile(files) {
    clearError();
    const list = $("[data-filelist]");
    if (!files || files.length === 0) return;
    if (files.length > 1) {
      drop?.reject("Drop one file at a time.");
      return;
    }
    const f = files[0];
    if (f.size > limits.maxBytes) {
      file = null;
      list.hidden = true;
      drop?.reject(`${f.name} is over the ${humanBytes(limits.maxBytes)} limit.`);
      return;
    }
    file = f;
    drop?.reset();
    list.hidden = false;
    $("[data-file-name]").textContent = f.name;
    $("[data-file-meta]").textContent = humanBytes(f.size);
    $("[data-file-icon]").className = "pb-icon pb-icon--file";
    setProgress(0);
  }

  function setProgress(pct) {
    const bar = $("[data-file-progress]");
    if (bar) {
      bar.dataset.value = String(pct);
      bar.style.setProperty("--value", `${pct}%`);
    }
  }

  // Passphrase generators.
  root.addEventListener("click", (e) => {
    const g = e.target.closest("[data-generate]");
    if (g) {
      passInput.value = g.dataset.generate === "words" ? generateWords() : generateChars();
      passInput.type = "text";
      const eye = passInput.parentElement.querySelector("[data-reveal]");
      if (eye) {
        eye.firstElementChild.className = "pb-icon pb-icon--eye-off";
        eye.setAttribute("aria-label", "Hide password");
      }
    }
    if (e.target.closest("[data-again]")) {
      result.hidden = true;
      form.hidden = false;
      $("#secret-link").value = "";
      textarea?.focus();
    }
  });

  const setBusy = (on, label = "Create secret") => {
    busy = on;
    submit.disabled = on;
    submit.setAttribute("aria-busy", String(on));
    submitIcon.className = on ? "pb-icon pb-icon--refresh pb-icon--spin" : "pb-icon pb-icon--lock";
    submitLabel.textContent = label;
  };

  root.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (busy) return;
    clearError();
    let views = viewsSel.value === "custom" ? Number(root.querySelector('input[name="views_custom"]').value) : Number(viewsSel.value);
    if (!Number.isInteger(views) || views < 1 || views > limits.maxViews) {
      fieldError(root.querySelector('input[name="views_custom"]'), `Views must be 1 to ${limits.maxViews}.`);
      return;
    }
    const input = {
      kind: kind(),
      text: textarea?.value ?? "",
      file,
      views,
      expiresIn: Number(root.querySelector('select[name="expires"]').value),
      passphrase: passInput.value,
      note: root.querySelector('input[name="note"]')?.value ?? "",
      notify: root.querySelector('input[name="notify"]')?.checked ?? false,
    };
    setBusy(true, "Starting…");
    try {
      const link = await createSecret(input, limits, {
        origin: location.origin,
        onStep: (label, done, total) => {
          setBusy(true, label);
          if (input.kind === e2e.KIND_FILE && total > 0) {
            const pct = Math.round((done / total) * 100);
            setProgress(pct);
            $("[data-file-meta]").textContent = `${humanBytes(file.size)} · ${pct}%`;
          }
        },
      });
      $("#secret-link").value = link;
      $("[data-result-pass]").hidden = !input.passphrase;
      // The plaintext leaves the page with the form.
      if (textarea) textarea.value = "";
      if (fileInput) fileInput.value = "";
      passInput.value = "";
      passInput.type = "password";
      const note = root.querySelector('input[name="note"]');
      if (note) note.value = "";
      file = null;
      const list = $("[data-filelist]");
      if (list) list.hidden = true;
      form.hidden = true;
      result.hidden = false;
      $("#secret-link").focus();
      $("#secret-link").select();
    } catch (err) {
      if (err instanceof HTTPError && err.status === 0) {
        // The page's own checks (nothing written, no file, too big) answer on the field.
        if (input.kind === e2e.KIND_FILE) drop?.reject(err.message);
        else fieldError(textarea, err.message);
        return;
      }
      const msg = err instanceof HTTPError ? err.message : "Network error. Check the connection and try again.";
      showError("The secret was not created.", msg);
    } finally {
      setBusy(false);
    }
  });
  // The button starts disabled (and the form is method="dialog"), so
  // nothing can be submitted before this handler is in place.
  submit.disabled = false;
}

if (globalThis.document) {
  for (const root of document.querySelectorAll("[data-secret-composer]")) composer(root);
}
