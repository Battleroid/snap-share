Resizable panels split a view into two panes with a draggable divider.

## Markup
- `.pb-split[data-resizable]` with `.pb-split__pane`, `button.pb-split__handle[aria-label]`, `.pb-split__pane`. `pb-split--vertical` stacks them.
- Options: `data-start` (first pane %, default 40), `data-min` / `data-max` (%, defaults 15 and 85), `data-key` (remember the size in `localStorage`, under `pb-split-<key>`). The size is the `--pb-split` property, which the script sets through the CSSOM.
- Use it for file list + preview, editor + output. Each pane scrolls on its own.

## States
- While dragging the split carries `data-dragging`.
- Each pane that overflows is a named, focusable scroll region (`pbScroller`); name it with `aria-label`, otherwise it is "Pane 1" and "Pane 2".
- Under 640px stack panes instead.

## Keys
- Focus the handle: arrows move 2%, Shift+arrows 10% (Left/Right, or Up/Down for `--vertical`), Home and End go to the min and max, Enter resets to the start. Double-click also resets.
- The handle has `role="separator"`, `aria-orientation` and `aria-valuenow`/`aria-valuemin`/`aria-valuemax`.

## Events
None; the component fires no events of its own. The size is in `--pb-split` and, with `data-key`, in `localStorage`.

## Do and don't
- Do: use it for file list + preview, editor + output.
- Do: give the handle and each pane an `aria-label`.
- Don't: set the size with an inline `style`; use `data-start`.
