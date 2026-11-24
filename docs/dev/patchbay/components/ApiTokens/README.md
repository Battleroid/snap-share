The API tokens page lists tokens, creates them with scopes and shows each one once.

## Markup

- List: a Data table with name and the last four characters, scopes as `ul.pb-scopes` of `li.pb-scope[data-access=read|write]` (`b` resource, `span` access; `pb-scope--admin` for admin), which wrap as even rows, last used as relative time, and expiry. Each row has Revoke.
- Create: a Dialog with name, an access list (`ul.pb-access`: one `li[data-res]` per resource with a none / read / write segmented control and a hint), a separate dashed admin checkbox that overrides the rows, and expiry (no "never" by default).
- After creating, show the token in an ok Notice with a CopyField: "Copy your new token now. You won't be able to see it again."
- Store only a hash; show `pb_…9f3c` afterwards.

## States

- Expiry: a warn badge inside 7 days, a down badge when expired.
- Revoking is immediate.

## Keys

Native controls inside the Dialog and rows: Tab and Space/Enter as for each element. The segmented controls follow Segmented; the CopyField button is a normal button.

## Events

None; the page fires no events of its own. The Dialog and CopyField behave as documented for those components; Revoke goes through a danger confirm (see ConfirmAction), then a toast.

## Do and don't

- Do: show a token once, at creation.
- Do: confirm Revoke with a danger confirm, then toast.
- Don't: offer "never" as the default expiry.
- Don't: store the token itself; only a hash.
