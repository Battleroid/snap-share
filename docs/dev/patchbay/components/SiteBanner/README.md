Site banners are full-width announcements above the app header.

## Markup

- `div.pb-sitebar` with an icon, a `p` (with an optional link) and an optional dismiss button.
- `data-dismiss` on the button (here, on a Notice or a toast) removes the banner; if focus was on it, focus moves to the next focusable thing after it (`pbFocusNear`), not to the top of the page.
- Dismissed banners stay dismissed: store the id.

## States

- Default is the accent ground; `data-status="warn|down|ok"` uses the pop colors.
- Outage banners cannot be dismissed.

## Keys

- Roles: `region` with a label for announcements, `alert` for outages, `status` for confirmations.
- After dismissing, focus moves to the next focusable element, not the page top.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: use it for maintenance, being offline, a full pool, or an update. One banner at a time.
- Don't: use it for problems with one item; those belong in a Notice on that page.
