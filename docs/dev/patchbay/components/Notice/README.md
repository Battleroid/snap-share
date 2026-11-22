Notices are inline messages inside a page or form: tint ground, full outline, icon, title, optional body and actions.

- Consumer provides: `div.pb-notice` with `data-status="ok|warn|down"` (none = info), a matching `pb-icon--status-*`, `p.pb-notice__title`, optional `p.pb-notice__body` and `.pb-notice__actions`, optional dismiss `pb-btn--ghost pb-btn--icon` last.
- `role="status"` for info, ok, warn; `role="alert"` for down.
- Title states the fact; body says the cause or the next step.
- For page-wide state, use the StatusPage banner; for a card's own warning, use Card's band.
