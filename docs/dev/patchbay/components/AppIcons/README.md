App icons are the favicon, home screen icons and browser theme color for each app.

- Files (in the export under `app-icons/`): `favicon.svg` (switches to the dark accent when the system is dark), `favicon-32.png`, `apple-touch-icon.png` (180, square; iOS rounds it), `icon-192.png`, `icon-512.png`, `icon-maskable-512.png` (the mark inside the 80% safe zone on a full accent square) and `site.webmanifest`.
- Head tags: the SVG icon first, the 32px PNG fallback, the touch icon, the manifest, and two `theme-color` metas (paper `#faf8f3` for light, `#14120e` for dark) so the browser bar matches the theme.
- Each app swaps the mark, not the frame: accent square, one white shape, one pop-yellow detail. Keep marks readable at 16px: one shape, no text.
