Buttons trigger actions; `pb-btn` plus one modifier sets intent.

## Markup

- A `<button>` (or `<a>` for navigation) with `pb-btn`, optional modifier, optional leading `<span class="pb-icon pb-icon--NAME">` and a short verb label. Icons are CSS masks over `data:` URIs: the page CSP needs `img-src data:`.
- Modifiers: `pb-btn--primary` (`accent` fill), `pb-btn--danger` (`pop-red` fill), `pb-btn--ghost` (tertiary, cancel), none = secondary. Sizes: default `size-control`, `pb-btn--sm`. Square icon-only: `pb-btn--icon` plus `aria-label`.

## States

- Primary is one per view; danger is for destructive actions only.
- Busy: set `aria-busy="true"`, swap the icon to `pb-icon--refresh pb-icon--spin`, change the label to the ongoing verb ("Syncing"); or use `pbBusy(true, button)` (see BusyRegion). Disabled: the `disabled` attribute.
- Toggle: `aria-pressed="true|false"` on a secondary or icon button keeps it pressed (`accent-tint` fill, `accent` border); flip it on click. Works inside `pb-btngroup` for a set of independent toggles.

## Keys

Native `button`: Tab, Space and Enter. Icon-only buttons need `aria-label`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: write labels as verbs in sentence case ("Deploy", "Copy link"), no trailing punctuation, no emoji.
- Don't: put two primary buttons side by side.
- Don't: use danger for "Cancel".
