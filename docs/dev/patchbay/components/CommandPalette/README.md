The command palette is a ⌘K search for pages, hosts and actions.

- Markup: `dialog.pb-dialog.pb-cmd[id]` with `pb-cmd__search` (icon, input, `esc` key hint), `ul.pb-cmd__list` of `li.pb-cmd__group` headings and `li.pb-cmd__item` (`data-cmd`, `data-keywords`, icon, `pb-cmd__label`, `pb-cmd__hint`), a hidden `pb-cmd__empty` and `pb-cmd__foot`.
- `pbCommand(dlg)` opens it on ⌘K / Ctrl+K and from any `[data-cmd-open=id]` button, filters as you type (prefix and fuzzy, keywords count), highlights the match, and handles ↑/↓/Enter/Esc. It fires a `command` event with `data-cmd` in `detail`.
- Groups: Jump to (pages, services) first, then Actions. Show the item's shortcut in `pb-cmd__hint`.
- Put the trigger in the header as the search button with the `⌘K` hint.
