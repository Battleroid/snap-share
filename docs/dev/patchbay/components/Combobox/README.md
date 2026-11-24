Combobox is a text field with a filtered list under it: pick one value from many by typing.

## Markup

- `div.pb-combo[data-combo]` > `.pb-input` (optional leading icon, `<input role="combobox" aria-expanded aria-controls aria-autocomplete="list">`, a `data-combo-toggle` chevron button) + `ul.pb-listbox[role=listbox][hidden]` of `li.pb-option[role=option][data-value]` (icon, label, optional `.pb-option__meta`), optional `li.pb-listbox__group` headings, and a hidden `li.pb-option--empty` message.
- `pbCombo(el)` is bound by the `combo` hook on `[data-combo]`. Option ids come from `pbId`.

## States

- Typing filters and highlights the match in `mark`; groups with no matches hide; the empty message shows when nothing matches. The chosen option shows a check (`aria-selected`).
- Closed: `aria-expanded="false"`, listbox hidden.

## Keys

- ↑/↓ open the list and move the active option (`aria-activedescendant`, via `pbListKeys`). Home/End jump to the first or last option once an arrow key moved into the list (before that they move the caret).
- Alt+↓ opens, Enter picks, Esc closes. Blur closes. The toggle button is not in the tab order.

## Events

Picking an option sets the input's value to its `data-value` and fires a native bubbling `change` on the input. The component fires no `pb:` events.

## Do and don't

- Do: use it when the list is long or searchable (hosts, users, images).
- Don't: use it for under ~10 fixed options; use Select.
- Don't: use it for many values; use TagInput.
