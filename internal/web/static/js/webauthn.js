// Passkeys (WebAuthn). The server sends ceremony options as JSON (the
// PublicKeyCredential*OptionsJSON forms under "publicKey") and takes the
// credential back as PublicKeyCredential.toJSON() would give it; both are
// converted by hand in browsers without those methods.
//
// Markup (no inline handlers; everything is wired here):
//   button[data-webauthn="login"]  sign-in page: a passkey sign-in, then go
//     to the answer's redirect. data-next is where. The login field, with
//     autocomplete "webauthn", also gets an autofill (conditional) request
//     when the browser has one; the button aborts it before its own.
//   button[data-webauthn="mfa"]    /login/2fa: the passkey as second step.
//   form[data-webauthn="register"] adding a passkey: its fields (password,
//     code, passkey) start the ceremony; the new credential goes into
//     form[data-webauthn-finish] as "credential", which is then submitted.
//   button[data-webauthn="proof"]  inside a form that confirms it's you: a
//     passkey assertion into the form's input[name=passkey], then the form
//     is submitted (in a register form, the next click carries on).
//   [data-webauthn-error]          a Notice, the nearest one shows failures.
//   [data-webauthn-only]           hidden until the browser has WebAuthn.

export function fromB64url(s) {
  const b64 = String(s).replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64 + "===".slice((b64.length + 3) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

export function toB64url(buf) {
  const bytes = ArrayBuffer.isView(buf) ? new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength) : new Uint8Array(buf);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

const descriptors = (list) => (list || []).map((d) => ({ ...d, id: fromB64url(d.id) }));

// creationOptions turns the server's JSON into options for credentials.create().
export function creationOptions(json, PKC = globalThis.PublicKeyCredential) {
  const o = json.publicKey;
  if (typeof PKC?.parseCreationOptionsFromJSON === "function") return PKC.parseCreationOptionsFromJSON(o);
  return {
    ...o,
    challenge: fromB64url(o.challenge),
    user: { ...o.user, id: fromB64url(o.user.id) },
    excludeCredentials: descriptors(o.excludeCredentials),
  };
}

// requestOptions turns the server's JSON into options for credentials.get().
export function requestOptions(json, PKC = globalThis.PublicKeyCredential) {
  const o = json.publicKey;
  if (typeof PKC?.parseRequestOptionsFromJSON === "function") return PKC.parseRequestOptionsFromJSON(o);
  return { ...o, challenge: fromB64url(o.challenge), allowCredentials: descriptors(o.allowCredentials) };
}

// credentialJSON is cred.toJSON(), by hand where the browser lacks it.
export function credentialJSON(cred) {
  if (typeof cred.toJSON === "function") return cred.toJSON();
  const r = cred.response;
  const response = { clientDataJSON: toB64url(r.clientDataJSON) };
  if (r.attestationObject) {
    response.attestationObject = toB64url(r.attestationObject);
    if (typeof r.getTransports === "function") response.transports = r.getTransports();
  } else {
    response.authenticatorData = toB64url(r.authenticatorData);
    response.signature = toB64url(r.signature);
    if (r.userHandle) response.userHandle = toB64url(r.userHandle);
  }
  const out = { id: cred.id, rawId: toB64url(cred.rawId), type: cred.type, response };
  if (cred.authenticatorAttachment) out.authenticatorAttachment = cred.authenticatorAttachment;
  out.clientExtensionResults = typeof cred.getClientExtensionResults === "function" ? cred.getClientExtensionResults() : {};
  return out;
}

// message is what to show for a failed ceremony; "" for nothing. A
// cancelled request reads the same whether or not a passkey exists.
export function message(err) {
  switch (err?.name) {
    case "AbortError":
      return "";
    case "NotAllowedError":
      return "The passkey request was cancelled or timed out.";
    case "InvalidStateError":
      return "This passkey is already added.";
    case "SecurityError":
      return "Passkeys don't work on this address.";
    case "Failure":
      return err.message;
    default:
      return "Your browser couldn't use a passkey.";
  }
}

export const isPath = (p) => typeof p === "string" && p.startsWith("/") && !p.startsWith("//") && !p.startsWith("/\\");

class Failure extends Error {
  constructor(msg) {
    super(msg);
    this.name = "Failure";
  }
}

// post sends a form (URLSearchParams) or JSON (a string) and returns the JSON answer.
async function post(url, body, signal) {
  const headers = { Accept: "application/json" };
  if (typeof body === "string") headers["Content-Type"] = "application/json";
  let res;
  try {
    res = await fetch(url, { method: "POST", body, headers, credentials: "same-origin", redirect: "error", signal });
  } catch (err) {
    if (err?.name === "AbortError") throw err;
    throw new Failure("Could not reach the server. Try again.");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Failure(data.error || "Something went wrong.");
  return data;
}

const go = (path) => location.assign(isPath(path) ? path : "/");

function errorBox(el) {
  for (let n = el; n && n.querySelector; n = n.parentElement) {
    const box = n.querySelector("[data-webauthn-error]");
    if (box) return box;
  }
  return null;
}

function showError(el, msg) {
  const box = errorBox(el);
  if (!box) return;
  box.querySelector(".pb-notice__title").textContent = msg;
  box.hidden = !msg;
}

function busy(el, on) {
  if (!el) return;
  el.disabled = on;
  el.toggleAttribute("aria-busy", on);
}

const supported = () => typeof globalThis.PublicKeyCredential === "function" && !!navigator.credentials;

// Sign-in. An autofill request stays open while the page is; the button
// aborts it, and waits for it to end, before asking in a dialog.
let autofill = null; // { ctl: AbortController, done: Promise }

function startAutofill(button) {
  const ctl = new AbortController();
  let picked = false; // failures before the user picks a passkey stay quiet
  const done = (async () => {
    try {
      if (!(await PublicKeyCredential.isConditionalMediationAvailable?.())) return;
      if (!document.querySelector('input[autocomplete~="webauthn"]')) return;
      const opts = await post("/login/passkey/begin", new URLSearchParams({ next: button.dataset.next || "" }), ctl.signal);
      const cred = await navigator.credentials.get({ publicKey: requestOptions(opts), mediation: "conditional", signal: ctl.signal });
      picked = true;
      const data = await post("/login/passkey/finish", JSON.stringify(credentialJSON(cred)));
      go(data.redirect);
    } catch (err) {
      if (ctl.signal.aborted || !picked) return;
      showError(button, message(err));
      startAutofill(button);
    } finally {
      if (autofill?.ctl === ctl) autofill = null;
    }
  })();
  autofill = { ctl, done };
}

async function login(button) {
  if (autofill) {
    autofill.ctl.abort();
    await autofill.done;
  }
  showError(button, "");
  busy(button, true);
  try {
    const opts = await post("/login/passkey/begin", new URLSearchParams({ next: button.dataset.next || "" }));
    const cred = await navigator.credentials.get({ publicKey: requestOptions(opts) });
    const data = await post("/login/passkey/finish", JSON.stringify(credentialJSON(cred)));
    go(data.redirect);
  } catch (err) {
    showError(button, message(err));
    busy(button, false);
    startAutofill(button);
  }
}

// The second step after the password.
async function mfa(button) {
  showError(button, "");
  busy(button, true);
  try {
    const opts = await post("/login/2fa/passkey/begin");
    const cred = await navigator.credentials.get({ publicKey: requestOptions(opts) });
    const data = await post("/login/2fa/passkey/finish", JSON.stringify(credentialJSON(cred)));
    go(data.redirect);
  } catch (err) {
    showError(button, message(err));
    busy(button, false);
  }
}

// Adding a passkey: confirm it's you, then make the credential.
async function register(form) {
  const finish = document.querySelector("form[data-webauthn-finish]");
  const submit = form.querySelector('button[type="submit"]');
  showError(form, "");
  busy(submit, true);
  try {
    const opts = await post("/settings/security/passkeys/begin", new URLSearchParams(new FormData(form)));
    resetProof(form); // the proof is used up either way
    const cred = await navigator.credentials.create({ publicKey: creationOptions(opts) });
    finish.querySelector('input[name="credential"]').value = JSON.stringify(credentialJSON(cred));
    finish.submit();
  } catch (err) {
    resetProof(form);
    showError(form, message(err));
    busy(submit, false);
  }
}

// A passkey as proof for the form around button.
async function proof(button) {
  const form = button.closest("form");
  const input = form?.querySelector('input[name="passkey"]');
  if (!input) return;
  // The passkey stands in for a code, not for the password.
  const codes = [...form.querySelectorAll('input[name="code"]')];
  const required = codes.map((c) => c.required);
  const restore = () => codes.forEach((c, i) => { c.required = required[i]; });
  codes.forEach((c) => { c.required = false; });
  if (!form.reportValidity()) {
    restore();
    return;
  }
  showError(button, "");
  busy(button, true);
  try {
    const opts = await post("/settings/security/reauth/passkey/begin");
    const cred = await navigator.credentials.get({ publicKey: requestOptions(opts) });
    input.value = JSON.stringify(credentialJSON(cred));
  } catch (err) {
    restore();
    showError(button, message(err));
    busy(button, false);
    return;
  }
  codes.forEach((c) => { c.value = ""; });
  if (form.dataset.webauthn === "register") {
    // Making the passkey is a second ceremony, which browsers start only
    // from a click: show the proof as done and let Continue carry on.
    button.querySelector(".pb-icon").className = "pb-icon pb-icon--check";
    button.lastChild.textContent = "Passkey confirmed";
    button.removeAttribute("aria-busy");
    form.querySelector('button[type="submit"]')?.focus();
    return;
  }
  form.requestSubmit();
}

function resetProof(form) {
  const input = form.querySelector('input[name="passkey"]');
  if (input) input.value = "";
  const button = form.querySelector('[data-webauthn="proof"]');
  if (!button) return;
  button.querySelector(".pb-icon").className = "pb-icon pb-icon--key";
  button.lastChild.textContent = "Use a passkey";
  busy(button, false);
}

if (globalThis.document) {
  if (supported()) document.querySelectorAll("[data-webauthn-only]").forEach((el) => { el.hidden = false; });

  document.addEventListener("click", (e) => {
    const button = e.target.closest?.("button[data-webauthn]");
    if (!button) return;
    if (!supported()) {
      showError(button, "This browser can't use passkeys.");
      return;
    }
    const mode = button.dataset.webauthn;
    if (mode === "login") login(button);
    else if (mode === "mfa") mfa(button);
    else if (mode === "proof") proof(button);
  });

  document.addEventListener("submit", (e) => {
    const form = e.target;
    if (!form.matches?.('form[data-webauthn="register"]')) return;
    if (e.submitter?.value === "cancel") return; // closes the dialog
    e.preventDefault();
    if (!supported()) {
      showError(form, "This browser can't use passkeys.");
      return;
    }
    register(form);
  });

  // A dialog opened again starts clean.
  document.querySelectorAll('form[data-webauthn="register"]').forEach((form) => {
    form.closest("dialog")?.addEventListener("close", () => {
      showError(form, "");
      resetProof(form);
      busy(form.querySelector('button[type="submit"]'), false);
    });
  });

  const button = document.querySelector('button[data-webauthn="login"]');
  if (button && supported()) startAutofill(button);
}
