// Admin pages: settings Switches that apply at once, the burn-a-secret form,
// which drops the #key part of a pasted link before anything is sent, and the
// delete-user dialog opened by the users list.

// Switches: input.pb-switch[data-setting] posts {key, value} to
// /admin/settings/apply. Same-origin fetches pass the CSRF check; the
// response is JSON. On failure the switch flips back and says why.
document.addEventListener("change", async (e) => {
  const sw = e.target;
  if (!(sw instanceof HTMLInputElement) || !sw.matches("input.pb-switch[data-setting]")) return;
  const field = sw.closest(".pb-field");
  const help = document.getElementById(sw.getAttribute("aria-describedby") || "");
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
  if (msg) {
    sw.checked = !want;
    field?.setAttribute("data-invalid", "");
    help.textContent = "";
    const icon = document.createElement("span");
    icon.className = "pb-icon pb-icon--status-down pb-icon--16";
    icon.setAttribute("aria-hidden", "true");
    help.append(icon, msg);
    return;
  }
  field?.removeAttribute("data-invalid");
  help.textContent = "Saved.";
  clearTimeout(help._restore);
  help._restore = setTimeout(() => help.replaceChildren(...help._orig.map((n) => n.cloneNode(true))), 2000);
});

// Burn form: the Burn… button is the shared confirm (app.js asks, then
// submits as the button). Before that runs, strip the fragment (the
// decryption key) so only the id leaves the browser, and check the field so
// an empty one is not asked about. Enter in the field presses Burn… (the
// form's default button is a disabled one, so Enter alone never burns).
const burnForm = document.querySelector("form[data-burn-form]");
if (burnForm) {
  const ref = burnForm.querySelector('input[name="ref"]');
  const burn = burnForm.querySelector("button[data-confirm-title]");
  const strip = () => { ref.value = ref.value.split("#")[0].trim(); };
  // On the form, so it runs before app.js's document-level confirm handler.
  burnForm.addEventListener("click", (e) => {
    if (e.target.closest("button[data-confirm-title]") !== burn) return;
    strip();
    if (!burnForm.reportValidity()) {
      e.preventDefault();
      e.stopPropagation();
    }
  });
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
