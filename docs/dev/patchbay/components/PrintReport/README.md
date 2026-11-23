Print styles make any page print cleanly, and the report layout is for documents meant to be printed.

- Printing hides the shell header, nav and footer, banners, toasts, bulk bars, pagers and buttons (and anything with `pb-noprint`), switches to black on white, removes shadows, lets scroll areas grow to full height, keeps cards, tables and events from splitting, and prints link URLs after links in prose and reports. Status colors and charts keep their color.
- `pb-print-only` shows content only on paper (a footer with the URL and date).
- Report: `article.pb-report` with `pb-report__head` (eyebrow, `pb-report__title`, a grid DetailList), then sections with `h2`. `pb-paper` (with `data-theme="light"`) previews it as a page on screen.
- Margins are 18mm by 16mm. Test with the browser's print preview in both themes.
