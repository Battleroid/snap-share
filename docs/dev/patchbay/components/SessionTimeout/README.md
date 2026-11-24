The session warning tells someone they are about to be signed out for being idle, and lets them stay.

- Markup: `dialog.pb-dialog.pb-dialog--sm.pb-session` with a warn icon, a title and a description that says what happens to unsaved work, `pb-session__count`, `pb-session__bar`, a hidden live region `[data-session-live]`, and `[data-session-out]` / `[data-session-stay]` buttons (Stay is primary and focused).
- `pbSession(dlg)` then `dlg.pbStart(seconds)` opens it modally and counts down; the bar and count turn red in the last 30 seconds; screen readers hear the time every 30 seconds and at 10. Esc does not dismiss it. It fires `pb:stay` or `pb:expire`.
- Show it two minutes before the timeout. Keep the session alive on any activity in the page, not only this button.
