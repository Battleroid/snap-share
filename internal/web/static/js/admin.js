// Admin pages: settings Switches that apply at once, the burn-a-secret form,
// which drops the #key part of a pasted link before anything is sent, the
// delete-user dialog opened by the users list, and the invites list's
// Copy link and active/all filter.

import { pbToast } from "../vendor/patchbay/patchbay.mjs";

// Switches: input.pb-switch[data-setting] posts {key, value} to
// /admin/settings/apply. Same-origin fetches pass the CSRF check; the
// response is JSON. On failure the switch flips back and says why.
document.addEventListener("change", async (e) => {
  const sw = e.target;
  if (!(sw instanceof HTMLInputElement) || !sw.matches("input.pb-switch[data-setting]")) return;
  const field = sw.closest(".pb-field");
  const help = document.getElementById(`${sw.id}-help`);
  if (help && !help._orig) help._orig = [...help.childNodes].map((n) => n.cloneNode(true));
  const want = sw.checked;
  sw.disabled = true;
  let msg = "";
  try {
    const res = await fetch("/admin/settings/apply", {
      method: "POST",
      headers: { Accept: "application/json" },
      body: new URLSearchParams({ key: sw.dataset.setting, value: String(want) }),
      credentials: "same-origin",
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok) msg = out.error || "Not saved.";
    else sw.checked = Boolean(out.on);
  } catch {
    msg = "Not saved: the server could not be reached.";
  }
  sw.disabled = false;
  if (!help) return;
  // The error sits on the field (.pb-field__error, aria-invalid, named by aria-describedby);
  // the help line only says "Saved." for a moment.
  const errId = `${sw.id}-error`;
  let err = document.getElementById(errId);
  if (msg) {
    sw.checked = !want;
    field?.setAttribute("data-invalid", "");
    sw.setAttribute("aria-invalid", "true");
    if (!err) {
      err = document.createElement("span");
      err.className = "pb-field__error";
      err.id = errId;
      help.before(err);
      sw.setAttribute("aria-describedby", `${help.id} ${errId}`);
    }
    const icon = document.createElement("span");
    icon.className = "pb-icon pb-icon--status-down pb-icon--16";
    icon.setAttribute("aria-hidden", "true");
    err.replaceChildren(icon, msg);
    sw.focus();
    return;
  }
  err?.remove();
  sw.removeAttribute("aria-invalid");
  sw.setAttribute("aria-describedby", help.id);
  field?.removeAttribute("data-invalid");
  help.textContent = "Saved.";
  clearTimeout(help._restore);
  help._restore = setTimeout(() => help.replaceChildren(...help._orig.map((n) => n.cloneNode(true))), 2000);
});

// Burn form: the Burn… button is the shared confirm (the kit's confirm hook
// asks, then clicks it again). Before it asks, strip the fragment (the
// decryption key) so only the id leaves the browser, and check the field so
// an empty one is not asked about. Enter in the field presses Burn… (the
// form's default button is a disabled one, so Enter alone never burns).
const burnForm = document.querySelector("form[data-burn-form]");
if (burnForm) {
  const ref = burnForm.querySelector('input[name="ref"]');
  const burn = burnForm.querySelector("button[data-confirm]");
  const strip = () => { ref.value = ref.value.split("#")[0].trim(); };
  // Capturing on the form, so it runs before the confirm hook on the button.
  burnForm.addEventListener("click", (e) => {
    if (e.target.closest("button[data-confirm]") !== burn) return;
    strip();
    if (!burnForm.reportValidity()) {
      e.preventDefault();
      e.stopPropagation();
    }
  }, true);
  burnForm.addEventListener("submit", strip);
  ref.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    burn?.click();
  });
}

// The users list links Delete… to /admin/users/{id}#delete-user: open that
// page's type-to-confirm dialog.
if (location.hash === "#delete-user") {
  const dialog = document.getElementById("delete-user");
  if (dialog instanceof HTMLDialogElement && !dialog.open) dialog.showModal();
}

// A dialog that came back from the server with errors in it opens at once.
document.querySelectorAll("dialog[data-autoopen]").forEach((d) => { if (!d.open) d.showModal(); });

// Invites: Copy link (button[data-invite-link], a POST form for use without
// scripts) asks the server for the link, which is never in the page, and
// shows it in the dialog's CopyField. Closing the dialog empties it again.
const linkDialog = document.getElementById("invite-link-dialog");
if (linkDialog instanceof HTMLDialogElement) {
  const field = linkDialog.querySelector("#invite-link-again");
  const title = linkDialog.querySelector("#invite-link-title");
  linkDialog.addEventListener("close", () => { field.value = ""; });
  document.addEventListener("click", async (e) => {
    const b = e.target.closest?.("button[data-invite-link]");
    if (!b || !b.form) return;
    e.preventDefault();
    b.closest("[popover]")?.hidePopover?.();
    let url = "", msg = "";
    try {
      const res = await fetch(b.form.action, { method: "POST", headers: { Accept: "application/json" }, credentials: "same-origin" });
      const out = await res.json().catch(() => ({}));
      if (res.ok && out.url) url = out.url;
      else msg = out.error || "The link could not be loaded.";
    } catch {
      msg = "The server could not be reached.";
    }
    if (!url) {
      pbToast({ status: "down", title: msg });
      return;
    }
    field.value = url;
    title.textContent = `Invite link · ${b.dataset.inviteLink}`;
    linkDialog.showModal();
    field.select();
  });
}

// Audit: the kit's table truncates its cells and redraws them on sort and expand, so the
// title (the full value) is set when a cut cell is pointed at or focused. The expanded row
// holds every field in full.
function titleCut(e) {
  const td = e.target.closest?.(".app-audit td.pb-dt__cell");
  if (td && !td.title && td.scrollWidth > td.clientWidth) td.title = td.textContent;
}
document.addEventListener("pointerover", titleCut);
document.addEventListener("focusin", titleCut);
