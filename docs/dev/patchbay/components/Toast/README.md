Toasts are short, temporary messages about something that just happened, stacked in the bottom-right corner.

- Script: `pbToast({title, body, status, actions, timeout, ink, container})` returns the toast; `toast.update({...})` changes it (loading to done), `toast.close()` removes it. `status`: `info` (default), `ok`, `warn`, `down`, `busy` (spinner, no timer).
- `actions`: up to two `{label, onClick}`; the first is a secondary button, the second ghost. Undo is the main use: perform the action at once, offer Undo in the toast.
- Timing: 5 s, 8 s for `down`; `timeout: 0` keeps it until dismissed. The timer bar pauses while the pointer or focus is on the toast. Escape or the X dismisses. At most four show; the oldest goes first.
- Roles: `status` (polite) for most, `alert` for `down`. The region is an `ol.pb-toasts` with `aria-live="polite"`; `pb-toasts--top` puts it top-right.
- `pb-toast--ink` is a dark toast for neutral confirmations (copied, moved).
- Use toasts for results of the user's own actions. Use Notice for problems that need attention on the page, and Notifications for things that happened while they were away. Never put the only copy of an error in a toast.
