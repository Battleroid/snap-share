Resizable panels split a view into two panes with a draggable divider.

- Markup: `.pb-split[data-resizable]` with `.pb-split__pane`, `button.pb-split__handle[aria-label]`, `.pb-split__pane`. `pb-split--vertical` stacks them.
- Options: `data-start` (first pane %, default 50), `data-min` / `data-max` (%), `data-key` (remember the size in `localStorage`). The size is the `--pb-split` property.
- Keyboard: focus the handle; arrows move 2%, Shift+arrows 10%, Enter resets. The handle has `role="separator"` and `aria-valuenow`.
- Use it for file list + preview, editor + output. Each pane scrolls on its own. Under 640px stack panes instead.
- Each pane that overflows is a named, focusable scroll region (`pbScroller`).
