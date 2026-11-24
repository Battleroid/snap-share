Notices are inline messages inside a page or form: tint ground, full outline, icon, title, optional body and actions.

## Markup
- `div.pb-notice` with `data-status="ok|warn|down"` (none = info), a matching `pb-icon--status-*`, `p.pb-notice__title`, optional `p.pb-notice__body` and `.pb-notice__actions`, optional dismiss `pb-btn--ghost pb-btn--icon` last.
- `pbNotice({variant, title, body | bodyHTML, actions, dismissible})` returns the markup (`variant` is info, ok, warn or down; `body` is text and escaped, `bodyHTML` is markup you trust). Actions with an `href` (http, https, mailto, relative, `#` or `?`) become links, the rest buttons; the first button is the stronger one.
- `pbNoticeInto(container, opts)` puts it in a container (replacing what is there, or `append: true`) and returns the element. It also takes a Node as `body` and `onClick(event, notice)` on an action.

## States
- Tone is `data-status`: info (none), `ok`, `warn`, `down`.
- Dismissing removes the notice and moves focus to the container.

## Keys
- `role="status"` for info, ok, warn (polite); `role="alert"` for down (assertive).
- Native buttons and links only; the dismiss button is named from `pbStrings.dismiss`.

## Events
None; the component fires no events of its own. Use an action's `onClick`.

## Do and don't
- Do: title states the fact; body says the cause or the next step.
- Do: for page-wide state, use the StatusPage banner; for a card's own warning, use Card's band.
- Don't: use `bodyHTML` for anything you did not write; use `body`, which is escaped.
