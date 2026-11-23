The shortcut sheet lists keyboard shortcuts and opens with ?.

- Markup: `dialog.pb-dialog.pb-keys[id]` with a search input and `pb-keys__groups` of `section.pb-keys__group` (`h3`, `dl > div > dt + dd` with `pb-kbd` keys; write "then" between keys of a sequence). `data-keywords` on a row adds search words. A hidden `pb-keys__empty` shows when nothing matches.
- `pbShortcuts(dlg)` opens it on `?` outside text fields and from `[data-keys-open=id]` buttons, and filters as you type.
- Mirror the command palette: every shortcut there appears here, grouped the same way. Show ⌘ on Mac and Ctrl elsewhere.
