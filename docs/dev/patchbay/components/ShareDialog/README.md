The share dialog sets who can open a link and on what terms. It is snap's main flow.

- Markup: `dialog.pb-dialog.pb-share[data-share]` with the item (`pb-share__item`: thumbnail, title, size) and three `pb-share__sec` sections: link, link settings (`pb-share__on`, dimmed while sharing is off) and people.
- Link: a CopyField-style input `[data-share-link]` with a QR button `[data-share-qr]` (shows `pb-share__qr`) and `[data-share-copy]`; `[data-share-access]` picks anyone with the link, only people below, or off.
- Settings rows (`pb-share__row`: label, italic hint, control): expiry `[data-share-exp]` (1 day, 7 days, 30 days, never), password switch `[data-share-pw]` revealing `[data-share-sub="pw"]`, downloads `[data-share-dl]`, open limit `[data-share-limit]` revealing `[data-share-sub="limit"]`.
- People: `pb-share__invite` (username or email, role, Invite) and `ul.pb-share__people` of `li[data-who]` with an avatar, role select (can view / can edit) and remove. The owner row has no controls.
- `pbShare(dlg)` wires it and fires `change` with the full settings, plus `invite`, `remove` and `stop`. Stop sharing asks first and turns the link off; files are kept.
- Default to 7 days and downloads on. Show opens and last opened in the footer.
