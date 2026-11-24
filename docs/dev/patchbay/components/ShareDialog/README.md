The share dialog sets who can open a link and on what terms. It is snap's main flow.

## Markup
- `dialog.pb-dialog.pb-share[data-share]` with the item (`pb-share__item`: thumbnail, title, size) and three `pb-share__sec` sections: link, link settings (`pb-share__on`, dimmed while sharing is off) and people.
- Link: a CopyField-style input `[data-share-link]` with a QR button `[data-share-qr]` (shows `pb-share__qr`) and `[data-share-copy]`; `[data-share-access]` picks anyone with the link, only people below, or off.
- Settings rows (`pb-share__row`: label, italic hint, control): expiry `[data-share-exp]` (a Segmented radiogroup: 1 day, 7 days, 30 days, never), password switch `[data-share-pw]` revealing `[data-share-sub="pw"]`, downloads `[data-share-dl]`, open limit `[data-share-limit]` revealing `[data-share-sub="limit"]` with a number input `[data-share-limitn]`.
- People: `pb-share__invite` (`[data-share-invite]` username or email, `[data-share-role]`, `[data-share-add]`, an error line `[data-share-err]`) and `ul.pb-share__people` of `li[data-who]` with an avatar, role select (can view / can edit) and remove `[data-share-remove]`. The owner row has no controls. `[data-share-stop]` is Stop sharing.
- `pbShare(dlg)` (the `share` hook) wires it; `dlg.pbState()` returns the current settings.

## States
- Access off dims the settings (`data-off`), shows "sharing off" in the link field and Copy does nothing.
- The password and limit rows reveal their sub-rows when switched on.
- Invite checks the value (username or email) and refuses a duplicate with a message in `[data-share-err]`.
- Stop sharing asks first and turns the link off; files are kept.

## Keys
- Native controls: Tab and Space/Enter. Enter in the invite field adds the person.
- Copy announces "copied"; if the clipboard is unavailable the link is selected and the copy shortcut is shown. The QR button has `aria-expanded`.

## Events
- `pb:change` (bubbles) with the full settings: `{access, expires, password, limit, downloads, people}`. It fires on a user change, not for programmatic ones.
- `pb:invite` with `{who, role}`, `pb:remove` with `{who}`, and `pb:stop` (no detail).

## Do and don't
- Do: default to 7 days and downloads on.
- Do: show opens and last opened in the footer.
- Don't: let Stop sharing run without asking.
