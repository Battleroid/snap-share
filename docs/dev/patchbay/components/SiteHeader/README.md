Site headers are the title bar for a website or a docs site: brand, navigation and a few actions. Use AppShell for apps with a sidebar.

## Markup

- `header.pb-sitehead[data-sitehead]` > `pb-sitehead__bar` with `a.pb-sitehead__brand` (`.pb-logo` and name, optional `small`; add `pb-sitehead__brand--stack` to put the tagline under the name, left-aligned, centered only in the wide centered header), `ul.pb-sitenav` of `pb-sitenav__link` (`aria-current="page"` underlines the current one), `pb-sitehead__spacer`, and `pb-sitehead__actions` (search button `pb-sitehead__search`, theme switch, call to action, and the `button.pb-sitehead__toggle[data-site-menu]`). Add `nav.pb-sitehead__mobile` for the phone menu.
- Dropdowns: a `button.pb-sitenav__link[aria-controls=id]` with a chevron, followed by `div.pb-mega[id]` (`--cols` columns) of `a.pb-mega__item` (`pb-mega__ic` icon, `b` title, one-line description) and an optional `pb-mega__foot`. `pbSiteHead(el)` wires them.
- Variants: `pb-sitehead__util` (a thin utility bar as the header's first child: status, help, sign in; hidden on phones), `pb-sitehead--center` (links left, name centered, actions right), `nav.pb-subnav` (a scrolling row of sections under the bar), `pb-sitehead--sticky` (stays on top, translucent with blur, gains a rule once scrolled), `pb-sitehead--plain` (no ground, over a hero).

## States

- Responsive by its own width: under 1000px the search collapses to an icon; under 760px the links and actions fold into the phone menu (dropdowns become `details` groups). Keep an action visible on phones with `pb-sitehead__keep`.
- The theme switch stays reachable on phones: `pbSiteHead` adds a copy of it to the bottom of `.pb-sitehead__mobile` (or keep your own there); without a menu it stays in the bar.
- Under 480px the host under the brand name is hidden so the bar fits at 375px.

## Keys

- `pbSiteHead(el)` opens one dropdown at a time, moves focus in, supports ↓/↑ and Esc, and closes on outside clicks.
- `aria-current="page"` marks the current link.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: one primary action at most; keep top-level links to five or fewer.
- Don't: use it for apps with a sidebar; use AppShell.
