Popovers show a small panel next to the thing that opened it.

- Click popover: `button[popovertarget=id]` and `div.pb-popover[popover][id]` with `pb-popover__title`, text and `pb-popover__actions`. Native popover: Escape and outside clicks close it. `pbPopovers(root)` places it under the trigger, flipping to stay on screen.
- Hover card: `button.pb-trigger[data-hovercard=id]` opens `div.pb-popover.pb-hovercard` on hover or focus after a short delay. It is a preview only; everything in it must also be reachable by clicking through.
- Toggletip: `button.pb-tiptoggle[popovertarget]` with an info icon opens a short explanation. Use it instead of a tooltip when the text matters or is longer than a line.
- `pb-popover--inline` shows the panel in place (docs, previews).
- Popovers hold a few facts and at most two actions. Use a Dialog for anything longer or that needs a decision.
