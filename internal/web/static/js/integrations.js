import { pbToast } from "../vendor/patchbay/patchbay.mjs";

// A ShareX download makes a new key. Fetch it here so the page can say which
// key was made (the server names it in X-Snap-Key-Name); without script the
// form posts and the browser downloads the file as before.
for (const form of document.querySelectorAll("form[data-sharex]")) {
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = form.querySelector("button");
    btn.disabled = true;
    try {
      const res = await fetch(form.action, { method: "POST", credentials: "same-origin", headers: { Accept: "application/octet-stream" } });
      if (!res.ok) throw new Error(res.status === 409 ? "Too many API keys. Revoke one first." : "The uploader was not downloaded.");
      const file = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "")?.[1] || "uploader.sxcu";
      const url = URL.createObjectURL(await res.blob());
      const a = Object.assign(document.createElement("a"), { href: url, download: file });
      document.body.append(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 10000);
      pbToast({ status: "ok", title: `Created key ${res.headers.get("X-Snap-Key-Name") || "for ShareX"}` });
    } catch (err) {
      pbToast({ status: "down", title: err.message });
    } finally {
      btn.disabled = false;
    }
  });
}
