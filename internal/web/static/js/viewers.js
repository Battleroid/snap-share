// The viewers strip of a secret's timeline: a chip per distinct visitor that
// filters the events below it.

// filterViewers shows the events of one viewer ("all": every one) in the
// timeline next to a viewers strip, marks that chip pressed, and hides a day
// heading with no event left under it.
export function filterViewers(strip, which) {
  for (const b of strip.querySelectorAll("button[data-viewer]")) b.setAttribute("aria-pressed", String(b.dataset.viewer === which));
  let heading = null;
  let any = false;
  let shown = 0;
  const flush = () => { if (heading) heading.hidden = !any; };
  for (const li of strip.parentElement.querySelectorAll(".app-timeline > li")) {
    if (li.matches(".pb-timeline__day")) {
      flush();
      heading = li;
      any = false;
    } else {
      li.hidden = which !== "all" && li.dataset.viewer !== which;
      if (!li.hidden) { any = true; shown++; }
    }
  }
  flush();
  // The strip counts every event but the list is one page: say when none of this viewer's are on it.
  const note = strip.parentElement.querySelector("[data-viewers-empty]");
  if (note) note.hidden = shown > 0;
}

// chosen is the filter a click on button b asks for: its viewer, or everyone
// when that chip is already the pressed one.
export function chosen(b) {
  return b.getAttribute("aria-pressed") === "true" ? "all" : b.dataset.viewer;
}
