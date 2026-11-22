CopyField shows a value to copy (share link, token, path) with a Copy action, and an optional reveal for secrets.

- Consumer provides: `.pb-input` with an icon, `<input readonly>` (`type="password"` for secrets), optional `button.pb-input__btn[data-reveal]`, and `button.pb-input__action[data-copy]` (`pb-icon--copy` + "Copy") flush at the right edge. Inline: a ghost icon button with `data-copy-text` and a `pb-tip`.
- Script: copies with the Clipboard API; the button turns `ok` with a check and "Copied" for 1.6 s. If the browser refuses, the value is selected and the button says "Press ⌘C".
- Secrets shown once say so in the help line.
