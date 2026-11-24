Popovers show a small panel next to the thing that opened it.

## Markup
- Click popover: `button[popovertarget=id]` and `div.pb-popover[popover][id]` with `pb-popover__title`, text and `pb-popover__actions`. The `popover` hook places it under the trigger when it opens, flipping to stay on screen (`data-align` on the panel or trigger sets the alignment). `pbPopovers(root)` runs the hooks again for popovers you add to the page later.
- Hover card: `button.pb-trigger[data-hovercard=id]` opens `div.pb-popover.pb-hovercard`.
- Toggletip: `button.pb-tiptoggle[popovertarget]` with an info icon opens a short explanation.
- `pb-popover--inline` shows the panel in place (docs, previews).

## States
- Click popover: open or closed natively. The hover card is a manual popover that opens after a short delay (about 300 ms) and closes shortly after leaving (about 200 ms), so the pointer can move into it.
- Inline popovers are always shown.

## Keys
- Click popover and toggletip: Escape and outside clicks close them natively; focus returns to the trigger.
- Hover card: opens on hover or focus, closes on leave or blur, and Escape hides it. It is a preview only; everything in it must also be reachable by clicking through.

## Events
None of its own. The native `toggle` event fires on the panel when it opens or closes.

## Do and don't
- Do: hold a few facts and at most two actions.
- Do: use a toggletip instead of a tooltip when the text matters or is longer than a line.
- Don't: put something in a hover card that is not reachable otherwise.
- Don't: use a popover for anything longer or that needs a decision; use a Dialog.
