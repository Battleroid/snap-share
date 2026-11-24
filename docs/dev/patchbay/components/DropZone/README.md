DropZone accepts files by drag or browse and lists uploads under it.

## Markup

- Consumer provides: `<label class="pb-drop">` with a hidden `<input type="file">`, `.pb-drop__icon` (`pb-icon--upload`), `.pb-drop__title`, `.pb-drop__hint` (accepted types and size limit, with `<u>browse</u>`). The kit styles the zone; your code handles the drop and sets `data-state`.
- Uploads: `.pb-filelist` > `.pb-filerow` (icon, name, `.pb-filerow__meta`), with a `.pb-progress[data-value="62"]` bar (the `progress` hook sets `--value`; update it with `el.pbSet(n)`) while sending; swap the icon to `status-ok` when done. For a custom color use a class, never an inline `style`.
- Upload settings: wrap the drop zone in `.pb-dropset[data-dropset]` with a `.pb-dropset__box` above it. The box holds `button.pb-dropset__bar` (chevron, `.pb-dropset__summary`, "Change") and `.pb-dropset__panel` with one `.pb-dropset__row` per setting (label, then a `pb-seg`, `pb-select` or `pb-switch`).
  - The summary line is built from the controls: radios and selects carry `data-sum="expires {v}"`; switches carry `data-sum-on` / `data-sum-off` (leave one empty to say nothing in that state).
  - `data-dropset-reveal="id"` on a switch shows a `.pb-dropset__sub` row (a password field) while it is on.
  - `pbDropSet(el)` wires it (included in `pbInit`): the bar opens and closes the panel, `[data-dropset-reset]` restores the defaults, `[data-dropset-default]` saves the current settings as the defaults.
- Compact settings: add `pb-dropset--compact`. The panel becomes a three-column grid of `.pb-dropset__field` (a `.pb-dropset__label` above the control): Expires, Visibility and File names on the first row; Strip photo location, Password and the password field on the second. `pb-dropset__field--switch` is a switch with its label beside it. `data-dropset-enable="id"` on a switch enables that field's inputs instead of revealing it, so the row keeps its shape. The footer spans the full width. `pb-dropset__field--wide` spans two columns when a control needs the room. Pair it with `pb-drop--slim`, a one-line drop zone.

## States

- `data-state` on the zone: none = idle, `over` while a drag is over it (swap the icon to `download`, title to "Release to upload N files"), `error` for a rejected drop (say why in the hint).
- Compact settings: under 560px the grid drops to two columns with Expires and the password field full width, under 340px to one.
- "Save as default" shows "Saving…" until your code settles it with `pb:saved` or `pb:error`.
- Keep the panel closed by default: the summary tells people what will happen, and they open it only to change something.

## Keys

- The file input is the focus target; the zone shows a focus ring (`:focus-within`) and Space or Enter opens the file picker.
- Opening the settings panel focuses its first checked control; Esc closes the panel and returns focus to the bar. The bar carries `aria-expanded`; the summary is a polite live region.

## Events

- `[data-dropset]` fires `pb:change` with the settings. "Save as default" fires `pb:default` with the settings; settle it with `pb:saved` (the settings become what Reset returns to) or `pb:error` (`detail.message`) dispatched on the element. Nothing is saved or confirmed by the library.
- The zone itself fires nothing; use the file input's native `change` and your own `dragover` / `drop` handlers.

## Do and don't

- Do: say why a drop was rejected in the hint.
- Do: keep the settings panel closed until someone asks to change it.
- Don't: use `style="..."` for the progress value or icon color; use `data-value`, `data-vars="--x:y"` or a class.
