ButtonGroup joins related `pb-btn`s into one control; `pb-toolbar` lays out a row of controls.

- Consumer provides: `div.pb-btngroup` with two or more `button.pb-btn` children (any modifiers).
- Split button: a primary button followed by a `pb-btn--primary pb-btn--icon` with `pb-icon--chevron-down` that opens a Menu.
- Pager: small buttons with `aria-current="page"` on the current page; disable Previous on page 1.
- Toolbar: `div.pb-toolbar` holds selects, buttons, segmented controls and search; `.pb-toolbar__spacer` pushes the rest right.
- Group actions on the same object (Start, Stop, Restart). For mutually exclusive choices, use Segmented.
