Tooltips are short labels on hover and focus, set with `class="pb-tip"` and `data-tip="…"`.

## Markup

- The attribute goes on a focusable element; icon-only buttons also keep their `aria-label`.
- Script (the `tip` hook, in every bundle): `pbTip(el)` turns the tip into a `role="tooltip"` popover in the top layer, so `overflow` and `contain` never clip it. Changing `data-tip` updates it.

## States

- It shows on hover and on keyboard focus after 300 ms (`pbTip.delay`), sits above the trigger and flips below at the viewport edge (`pbPlace` with `side: 'top'`), and stays while the pointer moves onto it.
- Without script the CSS `::after` tip shows instead; `data-tip-open` keeps that one showing (the copy buttons use it for "Copied").

## Keys

- Esc dismisses it without moving focus (WCAG 1.4.13). The trigger gets `aria-describedby` pointing at the tip, unless the tip only repeats the trigger's `aria-label`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: one line, under six words, no punctuation.
- Don't: put the only copy of important information in a tooltip.
