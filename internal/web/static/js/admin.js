// Admin pages: settings Switches that apply at once, and the burn-a-secret
// form, which drops the #key part of a pasted link before anything is sent.

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

// Burn form: strip the fragment (the decryption key) so only the id leaves
// the browser, check the field, then ask for confirmation.
const burnForm = document.querySelector("form[data-burn-form]");
if (burnForm) {
  const ref = burnForm.querySelector('input[name="ref"]');
  const strip = () => { ref.value = ref.value.split("#")[0].trim(); };
  burnForm.addEventListener("submit", strip);
  document.querySelector("[data-burn-open]")?.addEventListener("click", (e) => {
    strip();
    if (!burnForm.reportValidity()) return;
    document.getElementById(e.currentTarget.dataset.burnOpen)?.showModal();
  });
}
