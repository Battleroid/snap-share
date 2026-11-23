The skip link lets keyboard users jump past the header and navigation.

- Markup: `<a class="pb-skip" href="#main">Skip to content</a>` as the first element in `<body>` (or the shell), pointing at `main` with `tabindex="-1"`.
- It is hidden until focused, then appears at the top-left as an accent button.
- Every app page needs one. Keep the text "Skip to content".
