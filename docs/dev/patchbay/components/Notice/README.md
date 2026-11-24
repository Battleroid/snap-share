Notices are inline messages inside a page or form: tint ground, full outline, icon, title, optional body and actions.

- Consumer provides: `div.pb-notice` with `data-status="ok|warn|down"` (none = info), a matching `pb-icon--status-*`, `p.pb-notice__title`, optional `p.pb-notice__body` and `.pb-notice__actions`, optional dismiss `pb-btn--ghost pb-btn--icon` last.
- `role="status"` for info, ok, warn; `role="alert"` for down.
- Title states the fact; body says the cause or the next step.
- For page-wide state, use the StatusPage banner; for a card's own warning, use Card's band.
- `pbNotice({variant, title, body | bodyHTML, actions, dismissible})` returns the markup (`variant` is info, ok, warn or down; `body` is text and escaped, `bodyHTML` is markup you trust). Actions with an `href` become links, the rest buttons.
- `pbNoticeInto(container, opts)` puts it in a container (replacing what is there, or `append: true`) and returns the element. It also takes a Node as `body` and `onClick(event, notice)` on an action; dismissing removes the notice and moves focus to the container.
