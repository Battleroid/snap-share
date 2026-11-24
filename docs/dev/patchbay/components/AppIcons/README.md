App icons are the favicon, home screen icons and browser theme color for each app.

## Markup

- Files (in the export under `app-icons/`): `favicon.svg` (switches to the dark accent when the system is dark), `favicon-32.png`, `apple-touch-icon.png` (180, square; iOS rounds it), `icon-192.png`, `icon-512.png`, `icon-maskable-512.png` (the mark inside the 80% safe zone on a full accent square) and `site.webmanifest` (`theme_color` `#1f3fd6`, `background_color` `#faf8f3`).
- Head tags: the SVG icon first, the 32px PNG fallback, the touch icon, the manifest, and two `theme-color` metas (paper `#faf8f3` for light, `#14120e` for dark) so the browser bar matches the theme.
- These are separate from the kit's own UI icons, which are CSS masks over `data:` URIs and need `img-src data:` in the page CSP. App icons are ordinary files linked from `<head>`.

## States

Light and dark: the SVG favicon and the two `theme-color` metas follow the system scheme. The PNGs do not.

## Keys

None; icons are not interactive.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: swap the mark, not the frame: accent square, one white shape, one pop-yellow detail.
- Do: keep marks readable at 16px.
- Don't: use text or more than one shape in the mark.
