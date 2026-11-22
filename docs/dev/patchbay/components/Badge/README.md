Badges show state: an icon, a color and a word, always all three.

- Consumer provides: `<span class="pb-badge" data-status="ok|warn|down|info|maint|unknown|paused"><span class="pb-icon"></span>word</span>`; the icon follows `data-status`.
- `pb-badge` (tint ground) is the default. `pb-badge--solid` (pop fill) is for one loud result per view: CI outcome, a release tag.
- `pb-status` is the inline form (icon + word in the status color) for tables and sentences. `pb-dot` is an 8px marker for dense lists; always next to a word. `pb-count` is a number bubble.
- `ok` and `down` differ by hue only, so the icon and word are required.
