// Notification settings.
//
// Channel Switches apply immediately: input[data-notify-channel] posts
// channel and enabled to /settings/notifications/channel; on failure the
// switch flips back and the server's message shows in [data-notify-error].
// On success the channel's grid column ([data-notify-col]) is enabled or
// disabled to match.
//
// The events grid (form[data-notify-events], the kit's pbNotify inside)
// saves with fetch and settles the kit's section state (pb:saved,
// pb:error). Its Test buttons are the kit's (data-test): pb:test posts to
// /settings/notifications/test, toasts the real result and settles the button.

import { pbToast } from "../vendor/patchbay/patchbay.mjs";

const errorBox = document.querySelector("[data-notify-error]");
const events = document.querySelector("form[data-notify-events]");

function showError(msg) {
  if (!errorBox) return;
  errorBox.querySelector(".pb-notice__title").textContent = msg;
  errorBox.hidden = !msg;
}

async function post(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { Accept: "application/json" },
    body,
    credentials: "same-origin",
  });
  let data = {};
  try { data = await res.json(); } catch { /* not JSON */ }
  return { ok: res.ok, data };
}

function setColumn(channel, on) {
  document.querySelectorAll(`[data-notify-col="${CSS.escape(channel)}"]`).forEach((el) => { el.disabled = !on; });
}

document.addEventListener("change", async (e) => {
  const sw = e.target.closest("input[data-notify-channel]");
  if (!sw) return;
  const on = sw.checked;
  sw.disabled = true;
  showError("");
  const body = new URLSearchParams({ channel: sw.dataset.notifyChannel, enabled: on ? "1" : "0" });
  try {
    const { ok, data } = await post("/settings/notifications/channel", body);
    if (ok) {
      setColumn(sw.dataset.notifyChannel, on);
    } else {
      sw.checked = !on;
      showError(data.error || "Could not save.");
    }
  } catch {
    sw.checked = !on;
    showError("Could not save. Check your connection.");
  } finally {
    sw.disabled = false;
  }
});

if (events) {
  events.addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      const { ok, data } = await post(events.action, new URLSearchParams(new FormData(events)));
      events.dispatchEvent(ok ? new CustomEvent("pb:saved") : new CustomEvent("pb:error", { detail: { message: data.error || "Could not save" } }));
    } catch {
      events.dispatchEvent(new CustomEvent("pb:error", { detail: { message: "Could not save. Check your connection." } }));
    }
  });
  // Discard restores the boxes; a change event lets the kit redraw the
  // "All events" row after the kit's own reset has run.
  events.addEventListener("reset", () => {
    setTimeout(() => setTimeout(() => events.querySelector("table")?.dispatchEvent(new Event("change", { bubbles: true }))));
  });

  // The kit grid fires pb:test {channel, button} and shows "Sending…" on the button until this
  // settles it with pb:saved or pb:error {message}. They do not bubble: the form listens for
  // the same names to settle its Save. The result is the server's: sent, or why not.
  events.addEventListener("pb:test", async (e) => {
    const grid = e.target.closest("[data-notify]") || e.target;
    let ok = false;
    let msg = "Test not sent. Check your connection.";
    try {
      const r = await post("/settings/notifications/test", new URLSearchParams({ channel: e.detail.channel }));
      ok = r.ok;
      msg = ok ? (r.data.message || "Test sent.") : (r.data.error || "Test not sent.");
    } catch { /* offline: the default message */ }
    pbToast({ status: ok ? "ok" : "down", title: msg });
    grid.dispatchEvent(new CustomEvent(ok ? "pb:saved" : "pb:error", { detail: { message: msg } }));
  });
}
