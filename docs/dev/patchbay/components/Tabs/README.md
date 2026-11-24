Tabs switch between views of one object.

## Markup

- `div.pb-tabs[role=tablist]` of `button.pb-tab[role=tab]` with `aria-selected` and `aria-controls`, and one `div.pb-tabpanel[role=tabpanel]` per tab (`hidden` when inactive).
- Tabs may carry an icon or a `pb-count`.
- URL fragment: `data-tab-hash` on the `.pb-tabs` (or on single tabs) writes the selected tab's panel id to the fragment with `history.replaceState`, and selects the tab whose panel id is in the fragment on load and on `hashchange`, so `/settings#panel-keys` opens that tab.
- `data-hides="<selector>"` on a tab hides those elements while it is selected (options that only apply to the other tabs), and shows them again otherwise.

## States

- Selected is `ink` with a 2px `accent` underline.
- Tabs without `aria-controls` (links) are left alone by the script.

## Keys

- WAI-ARIA tabs: only the selected `.pb-tab[aria-controls]` is in the tab order; ←/→ (↑/↓ with `aria-orientation="vertical"` on the list), Home and End move to a tab and select it.

## Events

- `pb:tabchange` with `detail: {tab, panel}` on each change.

## Do and don't

- Do: use 2–6 tabs, one or two words each.
- Don't: use tabs for filters that change a list in place; use Segmented.
