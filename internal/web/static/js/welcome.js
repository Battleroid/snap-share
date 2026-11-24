// The first-run welcome on the composer: shown to a user with no items until
// they dismiss it. The choice is kept in this browser only; storage that
// throws (a private window) just means it shows again.
const KEY = "snap-welcome-dismissed";
const box = document.querySelector("[data-welcome]");
if (box) {
  let dismissed = false;
  try {
    dismissed = localStorage.getItem(KEY) === "1";
  } catch {
    // storage unavailable
  }
  if (dismissed) {
    box.remove();
  } else {
    box.hidden = false;
    box.addEventListener("click", (e) => {
      if (!e.target.closest("[data-dismiss]")) return;
      try {
        localStorage.setItem(KEY, "1");
      } catch {
        // storage unavailable
      }
    });
  }
}
