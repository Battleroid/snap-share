Dialog uses the native `<dialog>` element, opened with `showModal()`.

- Consumer provides: `dialog.pb-dialog` > `form[method=dialog]` > `.pb-dialog__head` (`h3.pb-dialog__title`, close icon button), `.pb-dialog__body`, `.pb-dialog__foot` (ghost Cancel, then the action). Buttons with `value` close it and set `returnValue`.
- Titles are questions for confirmations ("Delete snapshot?"); the body names exactly what changes. Destructive confirms use `pb-btn--danger` labelled with the verb.
- Escape and Cancel close without acting.
