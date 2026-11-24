The session warning tells someone they are about to be signed out for being idle, and lets them stay.

## Markup
- `dialog.pb-dialog.pb-dialog--sm.pb-session` with a warn icon, a title and a description that says what happens to unsaved work, `pb-session__count`, `pb-session__bar`, a hidden live region `[data-session-live]`, and `[data-session-out]` / `[data-session-stay]` buttons (Stay is primary and focused).
- `pbSession(dlg)` (the `session` hook, for a dialog that is not open) wires it; then `dlg.pbStart(seconds)` opens it modally and counts down (default 120).

## States
- The bar and count turn red (`data-urgent`) in the last 30 seconds.
- At zero the dialog closes and `pb:expire` fires.

## Keys
- Stay takes focus when it opens. Esc does not dismiss it.
- Screen readers hear the time left every 30 seconds and at 10 seconds, through the live region.

## Events
- `pb:stay` (bubbles) when Stay is pressed.
- `pb:expire` (bubbles) when the countdown ends; it also fires with `detail: {manual: true}` when the sign-out button is pressed.

## Do and don't
- Do: show it two minutes before the timeout.
- Do: keep the session alive on any activity in the page, not only this button.
- Don't: let Esc or a backdrop click dismiss it; it does not.
