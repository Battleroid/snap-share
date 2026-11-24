Toasts are short, temporary messages about something that just happened, stacked in the bottom-right corner.

## Markup

- `pbToast({title, body, bodyHTML, status, variant, action, actions, timeout, ink, container, max})` returns the toast. `body` is text and is escaped; `bodyHTML` is markup you trust (`<code>` for a path); a DOM Node passed as `body` is appended. `toast.update({...})` changes it (loading to done), `toast.close()` removes it.
- The toast's own markup carries no inline style, so it works under `style-src 'self'`. Do not add `style` attributes in `bodyHTML`; use a class.
- `action: {label, onClick}` adds one button (`actions` takes up to two; the first is a secondary button, the second ghost). `onClick(toast)` runs, then the toast closes unless the action has `close: false`.
- `pbInit` puts the stack (`div.pb-toasts[role=region]`, labelled `pbStrings.notifications`) and two hidden live regions in the page up front. `pb-toasts--top` puts the stack top-right. `pb-toast--ink` is a dark toast for neutral confirmations (copied, moved).

## States

- `status`: `info` (default), `ok`, `warn`, `down`, `busy` (spinner, no timer); or `variant`: `success`, `warning`, `error`, `info`.
- Timing: 5 s; errors (`down` / `error`) and `busy` stay until dismissed or updated; `timeout` sets your own (`0` keeps it). A toast with an action lasts at least 10 s (default 10 s; `timeout: 0` keeps it).
- Queue: at most three show (`max` changes it); later toasts wait, hidden, and show in order as others close. A queued toast's `update()` and `close()` work.

## Keys

- The timer bar pauses while the pointer or focus is on the toast, and every timer pauses while focus is anywhere in the stack. Escape or the X dismisses, and focus moves to the next toast or the page instead of being lost.
- F8 (`pbToast.hotkey = 'F8'` changes it) moves focus to the newest toast's first button and, pressed again, back; the stack carries `aria-keyshortcuts`.
- The stack and the toasts are not live regions themselves; each toast, and each status change through `update()`, is spoken through `pbAnnounce` (assertively for errors).

## Events

None; the component fires no events of its own. Action buttons run their `onClick(toast)`.

## Do and don't

- Do: use toasts for results of the user's own actions. Undo is the main use: perform the action at once, offer Undo in the toast.
- Don't: use them for problems that need attention on the page (Notice) or for things that happened while the user was away (Notifications).
- Don't: put the only copy of an error in a toast, or make it the only way to undo or reach something; the same action must exist on the page (a trash view, the item's menu).
