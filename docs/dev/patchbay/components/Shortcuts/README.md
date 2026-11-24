The shortcut sheet lists keyboard shortcuts and opens with ?. `pbShortcuts` is the registry that binds the keys and fills the sheet.

## Markup

- Registry: `pbShortcuts({'/': '#search', 'n': '/new', 'g o': fn, 'mod+enter': {do: save, desc: 'Save', allowInInput: true}}, {scope, sheet, group})`. Returns `{add(map), refresh(), open(), destroy()}`.
- A value is a function `fn(event, entry)`, a selector (focuses an input or textarea, clicks anything else), a URL or path (starts with `/`, `./`, `../`, `?` or `http(s):`) or `{do, desc, group, allowInInput, hidden}`.
- Keys are letters, symbols, `enter`, `esc`, `space`, `up`, `down`, `left`, `right`, `tab`, with `mod`, `ctrl`, `alt`, `shift`, `meta` prefixes joined by `+`. `mod` is ⌘ on Apple and Ctrl elsewhere. `"g o"` is a sequence (one second between keys).
- Sheet markup: `dialog.pb-dialog.pb-keys[id]` with a search input and `pb-keys__groups` of `section.pb-keys__group` (`h3`, `dl > div > dt + dd` with `pb-kbd` keys). Hand-written groups stay; registered shortcuts are added after them. `data-keywords` on a row adds search words; a close button needs `data-close`.
- Entries with a `desc` (or a selector target with a label) are listed in the sheet, grouped by `group`. `?` opens the sheet; one is created if the page has none.
- Open it from a button with `data-pb-open="#id"`.

## States

- A hidden `pb-keys__empty` shows when nothing matches the search.
- `scope` limits the keys to an element.

## Keys

- Keys are ignored while typing in a field, except `mod+` combos, Esc and entries with `allowInInput`. While a modal dialog is open only those fire too.
- Selector targets get `aria-keyshortcuts`.
- Opening the sheet clears the search and focuses it.

## Events

- Listens for `pb:show` on the sheet to clear the search and focus it. It fires no events of its own.

## Do and don't

- Do: mirror the command palette; every shortcut there appears here, grouped the same way.
