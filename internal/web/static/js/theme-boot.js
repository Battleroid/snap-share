// Theme boot: a classic, blocking script in <head>, before the CSS, so the
// page paints in the chosen theme. The choice ("light", "dark" or
// "system", the default) is in localStorage "pb-theme"; app.js wires the
// theme switches. Stand-in for the kit's pb-theme-boot.js.
(function () {
  var root = document.documentElement, choice = "system";
  try { choice = localStorage.getItem("pb-theme") || "system"; } catch (e) { /* storage blocked */ }
  if (choice !== "light" && choice !== "dark") choice = "system";
  var dark = choice === "dark" || (choice === "system" && window.matchMedia && matchMedia("(prefers-color-scheme: dark)").matches);
  root.dataset.theme = dark ? "dark" : "light";
  root.dataset.themeChoice = choice;
})();
