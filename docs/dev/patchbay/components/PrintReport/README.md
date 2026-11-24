Print styles make any page print cleanly, and the report layout is for documents meant to be printed.

## Markup
- Report: `article.pb-report` with `pb-report__head` (eyebrow, `pb-report__title`, a grid DetailList), then sections with `h2`. `pb-paper` (with `data-theme="light"`) previews it as a page on screen.
- `pb-print-only` shows content only on paper (a footer with the URL and date); `pb-noprint` hides something on paper.
- Page margins are 18mm by 16mm (`@page`).

## States
- On paper the shell header, nav and footer, the site bar, skip link, toasts, bulk bars, the upload dock, page-head actions, pagers, buttons and anything with `pb-noprint` are hidden. Banners and notices are not hidden unless you add `pb-noprint`.
- It switches to black on white, removes shadows and lets scroll areas (tables, logs, terminals, diffs, file lists) grow to full height.
- Cards, fieldsets, tables, events, incidents, figures, code blocks and table rows do not split across pages; headings stay with what follows.
- Link URLs print after `http` links in prose and reports. Badges, pills, status marks, charts and bars keep their color.

## Keys
- None; printing has no keyboard behaviour of its own (use the browser's print command).

## Events
None; the component fires no events of its own.

## Do and don't
- Do: test with the browser's print preview in both themes.
- Do: put the URL and date in a `pb-print-only` footer.
- Don't: rely on color alone for status; the words still print.
