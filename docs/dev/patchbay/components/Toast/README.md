Toasts are short, temporary messages about something that just happened, stacked in the bottom-right corner.

- Script: `pbToast({title, body, bodyHTML, status, actions, timeout, ink, container})` returns the toast. `body` is text and is escaped; `bodyHTML` is markup you trust (`<code>` for a path); a DOM Node passed as `body` is appended. The toast's own markup carries no inline style, so it works under `style-src 'self'`; `toast.update({...})` changes it (loading to done), `toast.close()` removes it. `status`: `info` (default), `ok`, `warn`, `down`, `busy` (spinner, no timer).
- `actions`: up to two `{label, onClick}`; the first is a secondary button, the second ghost. Undo is the main use: perform the action at once, offer Undo in the toast.
- Timing: 5 s, 8 s for `down`; `timeout: 0` keeps it until dismissed. The timer bar pauses while the pointer or focus is on the toast. Escape or the X dismisses. At most four show; the oldest goes first.
- Roles: `status` (polite) for most, `alert` for `down`. The region is a `div.pb-toasts` with `role="region"`, an `aria-label` and `aria-live="polite"`, holding `div.pb-toast`s (not a list: each toast carries its own `status` or `alert` role); `pb-toasts--top` puts it top-right.
- `pb-toast--ink` is a dark toast for neutral confirmations (copied, moved).
- Use toasts for results of the user's own actions. Use Notice for problems that need attention on the page, and Notifications for things that happened while they were away. Never put the only copy of an error in a toast.
