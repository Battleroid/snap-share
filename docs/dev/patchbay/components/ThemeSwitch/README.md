The theme switch picks light, dark or the system setting for the page or one panel.

## Markup

- `data-theme="light|dark"` on `<html>` (whole app) or on any container (one panel). Every token has a light and a dark value; components need no changes. `night` and `on-night` are the same in both themes for media stages and ink panels.
- A `pb-seg pb-seg--sm pb-themeswitch` radiogroup with `data-theme-switch` and three radios valued `light`, `dark`, `system`. `pb-seg--icon` for the compact sun / moon / monitor form; give each radio an `aria-label`.
- `pbThemeSwitch(el)` wires it; scope is the closest `[data-theme-scope]`, else `<html>`. `pbTheme('dark', el)` sets it from code.
- To avoid a flash on load, put `<script src="pb-theme-boot.js"></script>` in `<head>` before the stylesheet: a plain blocking script (not `defer`, not a module). It reads `localStorage` `pb-theme` (`light`, `dark` or `system`), falls back to `<html data-theme-default="…">`, then to the system setting, and sets `data-theme` and `data-theme-choice` on `<html>`. It is a file, not an inline snippet, so it passes `script-src 'self'`.

## States

- The page-wide choice is kept in `localStorage` (`pb-theme`); `system` follows `prefers-color-scheme` live.

## Keys

- A native radiogroup: Tab to the group, arrows to change. Icon-only radios need an `aria-label`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: put the page-wide switch in the app header tools or user menu, and default to `system`.
