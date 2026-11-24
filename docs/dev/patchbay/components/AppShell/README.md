The app shell is the frame every app uses: header, side navigation, main area and footer.

## Markup

- Frame: `div.pb-shell[data-shell]` with `header.pb-shell__head`, `nav.pb-shell__nav`, `main.pb-shell__main` (inner `pb-shell__inner` caps the width), `footer.pb-shell__foot`. Put `a.pb-skip` first.
- Header: `pb-shell__navbtn` (menu, phones only, `data-nav-toggle`), `a.pb-brand` (`pb-brand__mark`, name, `pb-brand__host`), the search button `pb-shell__search`, then `pb-shell__tools` (notifications bell, theme switch, avatar). The button's hint: `<span class="pb-kbd" data-kbd="mod+K">⌘K</span>` shows ⌘K on Apple devices and Ctrl+K elsewhere.
- Navigation: `ul.pb-nav` of `a.pb-nav__item` with an icon and optional `pb-count` or `pb-dot`; `aria-current="page"` on the current one. A group is a `button.pb-nav__item[aria-expanded]` followed by `ul.pb-nav__sub`. `p.pb-nav__heading` splits sections.
- Page header: `header.pb-pagehead` with breadcrumbs, `h1.pb-pagehead__title`, an optional `pb-pagehead__meta` line (mono, muted: owner, size, updated time, a badge), the prose `pb-pagehead__desc` and `pb-pagehead__actions` (primary action last).
- `div.pb-layout` puts content and an `aside.pb-layout__aside` side by side.
- The shell sets `--pb-head-h` (56px, the header height). Sticky things inside `pb-shell__main` offset by it: `pb-settings__nav` and `pb-layout__aside` stick at `calc(var(--pb-head-h, 0px) + var(--space-4))`. Ids in the main area get `scroll-margin-top` for the same offset, so anchor jumps land below the header. Use the variable for your own sticky elements.
- Nav and header icons are CSS masks over `data:` URIs: the page CSP needs `img-src data:`.
- `pb-shell--contained` keeps the shell inside a box for previews. The demo marks the page it shows (Shares) with `aria-current="page"`.

## States

- Under 900px the nav becomes a fixed drawer from under the header to the bottom of the viewport (a long nav scrolls inside it) and `pb-layout` stacks its aside under the content. Inside a narrow container `pb-layout` stacks under 720px of its own width. `pb-pagehead` also collapses to one column under 640px.
- Drawer: open is `data-nav-open` on the shell with `aria-expanded` on the Menu button. A closed drawer is `visibility: hidden`, so its links leave the tab order. Widening past 900px closes it, so nothing stays inert.

## Keys

- `pbShell(el)` wires the Menu button. Opening the drawer moves focus to the first nav item, makes the rest of the shell `inert` (the Menu button stays live) and adds a `.pb-shell__scrim` over the content.
- Escape, the scrim, an outside click or Menu close it; Escape, the scrim and Menu return focus to Menu.

## Events

None; the shell fires no events of its own. The search button typically carries `data-pb-open` for the command palette.

## Do and don't

- Do: put `a.pb-skip` first.
- Do: use `--pb-head-h` for your own sticky elements.
- Don't: use `pb-shell--contained` outside previews.
