CopyField shows a value to copy (share link, token, path) with a Copy action, and an optional reveal for secrets.

## Markup

- `.pb-input` with an icon, `<input readonly>` (`type="password"` for secrets), optional `button.pb-input__btn[data-reveal]`, and `button.pb-input__action[data-copy]` (`pb-icon--copy` + "Copy") flush at the right edge.
- Inline: a ghost icon button with `data-copy-text` and a `pb-tip`. Other sources: `data-copy-value`, or `data-copy-target="#id"` to copy another element's value or text.

## States

- Copied: the button turns `ok` with a check and "Copied" for 1.6 s and announces it through `pbAnnounce`.
- Refused: if the browser refuses the Clipboard API, the value is selected and the button says "Press ⌘C".

## Keys

Native buttons: Tab, Space and Enter. The reveal button toggles the field between password and text.

## Events

None; the component fires no events. The result is announced to assistive tech.

## Do and don't

- Do: say in the help line when a secret is shown once.
- Don't: use it for values the user must edit.
