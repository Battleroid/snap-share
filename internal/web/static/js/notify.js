// Notification settings: the channel Switches apply immediately.
// input[data-notify-channel] posts channel and enabled to
// /settings/notifications/channel; on failure the switch flips back and the
// server's message shows in [data-notify-error].

const errorBox = document.querySelector("[data-notify-error]");

function showError(msg) {
  if (!errorBox) return;
  errorBox.querySelector(".pb-notice__title").textContent = msg;
  errorBox.hidden = !msg;
}

document.addEventListener("change", async (e) => {
  const sw = e.target.closest("input[data-notify-channel]");
  if (!sw) return;
  const on = sw.checked;
  sw.disabled = true;
  showError("");
  const body = new URLSearchParams({ channel: sw.dataset.notifyChannel, enabled: on ? "1" : "0" });
  try {
    const res = await fetch("/settings/notifications/channel", {
      method: "POST",
      headers: { Accept: "application/json" },
      body,
      credentials: "same-origin",
    });
    if (!res.ok) {
      let msg = "Could not save.";
      try { msg = (await res.json()).error || msg; } catch { /* not JSON */ }
      sw.checked = !on;
      showError(msg);
    }
  } catch {
    sw.checked = !on;
    showError("Could not save. Check your connection.");
  } finally {
    sw.disabled = false;
  }
});
