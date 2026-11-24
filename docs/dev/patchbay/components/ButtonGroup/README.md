ButtonGroup joins related `pb-btn`s into one control; `pb-toolbar` lays out a row of controls.

## Markup

- `div.pb-btngroup` with two or more `button.pb-btn` children (any modifiers). Groups may mix `button` and `a` elements; the ends are rounded by position among the `.pb-btn` children, not by element type.
- Split button: a primary button followed by a `pb-btn--primary pb-btn--icon` with `pb-icon--chevron-down` that opens a Menu.
- Pager: small buttons with `aria-current="page"` on the current page.
- Toolbar: `div.pb-toolbar` holds selects, buttons, segmented controls and search; `.pb-toolbar__spacer` pushes the rest right.

## States

Pager: disable Previous on page 1.

## Keys

Each child is a native control: Tab, Space and Enter.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: group actions on the same object (Start, Stop, Restart).
- Don't: use a group for mutually exclusive choices; use Segmented.
