Buttons trigger actions; `pb-btn` plus one modifier sets intent.

- Consumer provides: a `<button>` (or `<a>` for navigation) with `pb-btn`, optional modifier, optional leading `<span class="pb-icon pb-icon--NAME">`, and a short verb label.
- Modifiers: `pb-btn--primary` (one per view, `accent` fill), `pb-btn--danger` (`pop-red` fill, destructive only), `pb-btn--ghost` (tertiary, cancel), none = secondary. Sizes: default `size-control`, `pb-btn--sm`. Square icon-only: `pb-btn--icon` plus `aria-label`.
- Busy: set `aria-busy="true"`, swap the icon to `pb-icon--refresh pb-spin`, change the label to the ongoing verb ("Syncing"). Disabled: the `disabled` attribute.
- Labels are verbs in sentence case: "Deploy", "Copy link". No trailing punctuation, no emoji.
- Do not put two primary buttons side by side. Do not use danger for "Cancel".
