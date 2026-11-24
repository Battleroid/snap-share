Dialog is the native `<dialog>` element, opened with `showModal()`, in five shapes: confirm, type to confirm, form, long content and drawer.

## Markup

- Structure: `dialog.pb-dialog` > `form[method=dialog]` > `.pb-dialog__head` (optional `.pb-dialog__icon[data-status]`, a `div` with `h3.pb-dialog__title` and `p.pb-dialog__sub`, a ghost close icon button with `value="cancel"`) > `.pb-dialog__body` > `.pb-dialog__foot` (optional `.pb-foot__status`, ghost Cancel, then the action). Buttons with a `value` close it and set `returnValue`. The footer is the same `surface-sunk` strip as form sections.
- Sizes: `pb-dialog--sm` (400px, confirms), default (480px, short forms), `pb-dialog--lg` (720px). `pb-dialog--scroll` keeps head and foot fixed and scrolls the body. `pb-dialog--drawer` slides in from the right at full height, for editing one item beside a list.
- Open a static dialog with `data-pb-open="#dialog-id"` on a button (the same opener serves the sheet, the shortcut sheet and the palette). It calls `showModal()`, sets `aria-controls` and `aria-haspopup="dialog"` on the button, and then fires `pb:show` on the dialog.
- Confirm from script: `pbConfirm({title, body, bodyHTML, confirm, cancel, status, danger, typeToConfirm, container})` returns a Promise of `true` / `false`. `body` is text and is escaped; `bodyHTML` is markup you trust; a DOM Node passed as `body` is appended. `danger: true` uses `pb-btn--danger` and the `down` icon; `typeToConfirm: "riley"` keeps the action disabled until the name is typed.
- Close and acknowledge buttons use `<form method="dialog">` with `value`. If the page stops form submits globally, skip forms whose method is `dialog`, or the buttons stop closing it.
- A long body with `pb-prose` spans the full dialog width; the scroll bar sits at the dialog edge.
- Names: the `modal` hook gives every `<dialog>` `aria-labelledby` from its `.pb-dialog__title`, and `aria-describedby` from `.pb-dialog__sub` or, when the body has no form controls, the body's first paragraph, unless you set them yourself. `pbConfirm` dialogs are labelled by their title and described by their body, so `getByRole('dialog', {name})` finds them.

## States

- Five shapes: confirm, type to confirm, form, long content and drawer.
- Phones (under 600px): every dialog but the drawer becomes a bottom sheet with full-width footer buttons.
- While a modal dialog is open the page behind does not scroll; the scroll bar's width is kept as padding so nothing shifts.
- Type to confirm: the action stays disabled until the typed text matches.

## Keys

- `showModal()` makes the page behind inert and keeps Tab inside. Escape and Cancel close without acting.
- Put `autofocus` on a form dialog's first field; `pbConfirm` dialogs focus Cancel (or the type-to-confirm field).

## Events

- `pb:show` fires on the dialog after `showModal()` when a `data-pb-open` button opens it; it does not bubble and carries `detail.opener`. Listen for it to reset or fill the dialog.
- The native `close` event fires with `returnValue` set to the pressed button's `value`.

## Do and don't

- Do: write confirm titles as questions ("Delete snapshot?"), name exactly what changes and whether it can be undone, and repeat the verb on the action ("Delete user"), never "OK".
- Do: use `pbConfirm` for deleting users, pools or anything with data attached.
- Don't: stack dialogs; for a follow-up, close the first.
