Copy link and a QR code as one group.

- Markup: `<div data-sharelink="https://…" [data-show-link]></div>` fills itself (an empty element; with children it is left alone). `data-show-link` adds the link as plain mono text and an Open button. `pbShareLink({url, id, showLink})` returns the same markup for scripts.
- The group is a Copy link button (it flips to "Copied" through the copy hook) and a QR button that opens a popover with the code and the link. The link must be http(s) or site-relative; anything else shows no link text or Open.
- Use CopyField, not this, for one-time values such as new API keys.
- Words: `pbStrings.copyLink`, `open`, `qrCode`.
