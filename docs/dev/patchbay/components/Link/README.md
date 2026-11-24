Links are `accent` and underlined everywhere; never remove the underline in running text.

## Markup

- Consumer provides: `<a class="pb-link">`. Inside `.pb-prose` plain `<a>` is styled automatically, and `http…` links get the external mark.
- `pb-link--external` adds the `link-external` mark for links that leave the app. `pb-link--host` sets a host or URL in mono. `pb-link--quiet` is ink with a `line-strong` underline, for dense lists where a column of blue would be loud.

## States

- Links are `accent` and underlined everywhere. `pb-link--quiet` is ink with a `line-strong` underline and turns `accent` on hover.

## Keys

- Native `a`: Tab to focus, Enter to follow.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: say where the link goes ("restore guide").
- Don't: remove the underline in running text.
- Don't: write "click here".
