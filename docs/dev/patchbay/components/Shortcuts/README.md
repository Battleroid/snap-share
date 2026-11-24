The shortcut sheet lists keyboard shortcuts and opens with ?. `pbShortcuts` is the registry that binds the keys and fills the sheet.

- Registry: `pbShortcuts({'/': '#search', 'n': '/new', 'g o': fn, 'mod+enter': {do: save, desc: 'Save', allowInInput: true}}, {scope, sheet, group})`.
  - A value is a function `fn(event, entry)`, a selector (focuses an input or textarea, clicks anything else), a URL or path (starts with `/`, `./`, `../`, `?` or `http(s):`) or `{do, desc, group, allowInInput, hidden}`.
  - Keys are letters, symbols, `enter`, `esc`, `space`, `up`, `down`, `left`, `right`, `tab`, with `mod`, `ctrl`, `alt`, `shift`, `meta` prefixes joined by `+`. `mod` is ⌘ on Apple and Ctrl elsewhere. `"g o"` is a sequence (one second between keys).
  - Keys are ignored while typing in a field, except `mod+` combos, Esc and entries with `allowInInput`. While a modal dialog is open only those fire too. `scope` limits the keys to an element.
  - Entries with a `desc` (or a selector target with a label) are listed in the sheet, grouped by `group`. Selector targets get `aria-keyshortcuts`. `?` opens the sheet; one is created if the page has none.
  - Returns `{add(map), refresh(), open(), destroy()}`.
- Sheet markup: `dialog.pb-dialog.pb-keys[id]` with a search input and `pb-keys__groups` of `section.pb-keys__group` (`h3`, `dl > div > dt + dd` with `pb-kbd` keys). Hand-written groups stay; registered shortcuts are added after them. `data-keywords` on a row adds search words; a close button needs `data-close`. A hidden `pb-keys__empty` shows when nothing matches.
- Open it from a button with `data-pb-open="#id"`; `pb:show` clears the search and focuses it.
- Mirror the command palette: every shortcut there appears here, grouped the same way.
