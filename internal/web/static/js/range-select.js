// Shift-click range selection for lists of row checkboxes: Shift-clicking a
// box sets every box between it and the last box clicked to the clicked box's
// new state, as in a file manager.

// between lists boxes from anchor to target, both included, in page order.
// It is empty when either is not in boxes (the list was swapped since).
export function between(boxes, anchor, target) {
  const a = boxes.indexOf(anchor);
  const b = boxes.indexOf(target);
  if (a < 0 || b < 0) return [];
  return boxes.slice(Math.min(a, b), Math.max(a, b) + 1);
}

// rangeSelect watches clicks on boxes matching sel under root. boxes() lists
// the boxes in page order; changed() runs after a range was set (setting
// .checked from script fires no change event).
export function rangeSelect(root, sel, boxes, changed) {
  let anchor = null;
  root.addEventListener("click", (e) => {
    const box = e.target instanceof Element ? e.target.closest(sel) : null;
    if (!box) return;
    const last = anchor;
    anchor = box;
    if (!e.shiftKey || !last || last === box) return;
    const range = between(boxes(), last, box);
    if (!range.length) return;
    for (const b of range) if (!b.disabled) b.checked = box.checked;
    // Shift-click also extends the page's text selection; drop it.
    globalThis.getSelection?.()?.removeAllRanges();
    changed();
  });
}
