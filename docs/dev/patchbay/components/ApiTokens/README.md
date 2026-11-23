The API tokens page lists tokens, creates them with scopes and shows each one once.

- List: a Data table with name and the last four characters, scopes as `ul.pb-scopes` of `li.pb-scope[data-access=read|write]` (`b` resource, `span` access; `pb-scope--admin` for admin), which wrap as even rows, last used as relative time, and expiry (a warn badge inside 7 days, a down badge when expired). Each row has Revoke.
- Create: a Dialog with name, an access list (`ul.pb-access`: one `li[data-res]` per resource with a none / read / write segmented control and a hint), a separate dashed admin checkbox that overrides the rows, and expiry (no "never" by default). After creating, show the token in an ok Notice with a CopyField: "Copy your new token now. You won't be able to see it again."
- Revoke: a danger confirm, then a toast. Revoking is immediate.
- Store only a hash; show `pb_…9f3c` afterwards.
