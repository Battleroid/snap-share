// Hover previews: a short muted teaser plays over a poster while the mouse
// rests on it (composer recent uploads, dashboard grid, album galleries).
//
// Markup contract:
//
//   <div data-preview="/slug/preview"> … <span><img src="/slug/thumb" alt=""></span> … </div>
//
// - data-preview is the teaser URL (an MP4). That element is the hover target.
// - It contains an <img>, the poster. The teaser <video> is inserted right after
//   the img, in the img's parent, and covers it; that parent is marked
//   data-preview-frame (position: relative in app.css, unless it is already
//   positioned), so it should have the poster's size.
// - Elements added to the page later work too: events are delegated.
//
// Behaviour: a mouse pointer resting 250 ms inserts
// <video muted playsinline loop preload="auto"> over the poster. It fades in
// over 120 ms once it plays, with a thin pb-progress line along the bottom
// tracking playback. Leaving pauses and removes it, which restores the poster.
// One teaser plays at a time. Touch and pen pointers are ignored (a tap opens
// the item as usual). Under prefers-reduced-motion the teaser still plays (it
// only starts on a hover), without the fade. Nothing happens under Save-Data,
// or when the teaser fails to load (a 404 before the media job ran): the
// poster stays. Styling lives in app.css ("hover teasers").

const DELAY_MS = 250;
const failed = new Set(); // teaser URLs that did not load
let pending = null; // { host, timer }
let active = null; // { host, frame, video, progress, raf }

function enabled() {
  return !(navigator.connection && navigator.connection.saveData);
}

function hostOf(node) {
  return node instanceof Element ? node.closest("[data-preview]") : null;
}

function cancelPending() {
  if (!pending) return;
  clearTimeout(pending.timer);
  pending = null;
}

function stop() {
  cancelPending();
  if (!active) return;
  const a = active;
  active = null;
  cancelAnimationFrame(a.raf);
  a.video.pause();
  a.video.removeAttribute("src");
  a.video.load(); // drops the connection
  a.video.remove();
  a.progress.remove();
  delete a.frame.dataset.previewFrame;
}

function start(host) {
  const url = host.dataset.preview;
  const img = host.querySelector("img");
  if (!url || !img || failed.has(url) || !enabled() || !host.isConnected) return;
  stop();
  const frame = img.parentElement;
  const video = document.createElement("video");
  video.className = "app-preview";
  for (const name of ["muted", "playsinline", "loop"]) video.setAttribute(name, "");
  video.setAttribute("preload", "auto");
  video.setAttribute("aria-hidden", "true");
  video.muted = true;
  video.src = url;
  const progress = document.createElement("div");
  progress.className = "pb-progress app-preview-progress";
  progress.append(document.createElement("span"));
  frame.dataset.previewFrame = "";
  img.after(video);
  frame.append(progress);

  const a = { host, frame, video, progress, raf: 0 };
  active = a;
  const tick = () => {
    if (active !== a) return;
    const d = video.duration;
    if (d > 0 && Number.isFinite(d)) {
      progress.style.setProperty("--value", `${Math.min(100, (video.currentTime / d) * 100).toFixed(2)}%`);
    }
    a.raf = requestAnimationFrame(tick);
  };
  video.addEventListener("playing", () => {
    if (active !== a) return;
    video.dataset.state = "on";
    progress.dataset.state = "on";
    cancelAnimationFrame(a.raf);
    a.raf = requestAnimationFrame(tick);
  });
  video.addEventListener("error", () => {
    failed.add(url);
    if (active === a) stop();
  });
  video.play().catch(() => {
    if (active === a) stop();
  });
}

document.addEventListener("pointerover", (e) => {
  if (e.pointerType !== "mouse") return;
  const host = hostOf(e.target);
  if (!host || host.contains(e.relatedTarget)) return; // moving inside the host
  if (pending?.host === host || active?.host === host) return;
  cancelPending();
  if (!enabled()) return;
  pending = { host, timer: setTimeout(() => { pending = null; start(host); }, DELAY_MS) };
});

document.addEventListener("pointerout", (e) => {
  if (e.pointerType !== "mouse") return;
  const host = hostOf(e.target);
  if (!host || host.contains(e.relatedTarget)) return;
  if (pending?.host === host) cancelPending();
  if (active?.host === host) stop();
});

document.addEventListener("visibilitychange", () => { if (document.hidden) stop(); });
