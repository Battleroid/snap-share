The skip link lets keyboard users jump past the header and navigation.

## Markup

- `<a class="pb-skip" href="#main">Skip to content</a>` as the first element in `<body>` (or the shell), pointing at `main` with `tabindex="-1"`.

## States

- Hidden until focused, then it appears at the top-left as an accent button.

## Keys

Native link: it is the first Tab stop on the page and Enter follows it to `main`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: put one on every app page, and keep the text "Skip to content".
