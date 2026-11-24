Badges show state: an icon, a color and a word, always all three.

## Markup

- `<span class="pb-badge" data-status="ok|warn|down|info|maint|unknown|paused"><span class="pb-icon"></span>word</span>`; the icon follows `data-status`. Icons are CSS masks over `data:` URIs: the page CSP needs `img-src data:`.
- `pb-status` is the inline form (icon + word in the status color) for tables and sentences. `pb-dot` is an 8px marker for dense lists; always next to a word. `pb-count` is a number bubble.

## States

- `pb-badge` (tint ground) is the default. `pb-badge--solid` (pop fill) is for one loud result per view: CI outcome, a release tag.
- Status values: ok, warn, down, info, maint, unknown, paused.

## Keys

Not interactive. A bare `.pb-dot[data-status]` says its state in words (`role=img`, `aria-label` from `pbStrings.status`) unless it has its own label or is `aria-hidden`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: always give icon, color and word together.
- Don't: rely on color: `ok` and `down` differ by hue only, so the icon and word are required.
- Don't: use more than one solid badge per view.
