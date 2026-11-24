Tooltips are short labels on hover and focus, set with `class="pb-tip"` and `data-tip="…"`.

- Consumer provides: the attribute on a focusable element; icon-only buttons also keep their `aria-label`.
- Script (the `tip` hook, in every bundle): `pbTip(el)` turns the tip into a `role="tooltip"` popover in the top layer, so `overflow` and `contain` never clip it. It shows on hover and on keyboard focus after 300 ms (`pbTip.delay`), sits above the trigger and flips below at the viewport edge (`pbPlace` with `side: 'top'`), stays while the pointer moves onto it, and Esc dismisses it without moving focus (WCAG 1.4.13). The trigger gets `aria-describedby` pointing at it, unless the tip only repeats the trigger's `aria-label`. Changing `data-tip` updates it.
- Without script the CSS `::after` tip shows instead; `data-tip-open` keeps that one showing (the copy buttons use it for "Copied").
- One line, under six words, no punctuation. Never put the only copy of important information in a tooltip.
