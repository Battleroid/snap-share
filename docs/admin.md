# Admin

The admin area is at `/admin`, linked as **admin** in the header for admins. It has seven tabs: Overview, Users, Invites, Content, Secrets, Settings and Audit. Everything there is also logged in the audit log.

## Access

Only admins get in: a signed-in admin, or an API key with the `admin` scope (which only admins can create). Everyone else gets `403`. A key never sees set-password or invite links (they are only emailed) and can't make anyone an admin; see [api.md](api.md#admin).

Set `SNAP_ADMIN_CIDRS` (comma-separated addresses or CIDRs) to allow the admin pages and the admin API only from those networks, for example `SNAP_ADMIN_CIDRS=192.168.1.0/24`. Other pages are not affected. The address is the client address after `SNAP_TRUSTED_PROXIES` is applied.

Make the first admin with the CLI (see [accounts.md](accounts.md#creating-the-first-admin)) or through the SSO admin mapping.

## Overview

Tiles for active and disabled accounts, the number of items, storage used (with the free space on the files disk), active secrets, and uploads over the last 30 days with a per-day line. Uploads are counted from items that still exist. Below them, the latest audit entries.

## Users

A table of accounts, searchable by username or email, with role, status, whether SSO is linked, item count, storage used against the quota, and the last sign-in. The row menu has Edit, Reset password, Disable or Enable, and Delete.

- **New user**: a username, an optional email address and a role. With password sign-in on, snap issues a link to set a password, valid once for 24 hours. With SMTP configured and an address given, the link is emailed; otherwise it is shown once on the page for you to pass on. An address you enter is treated as verified.
- **Role**: `user` or `admin`.
- **Disable**: the account is signed out everywhere, its API keys are revoked (enabling it again doesn't bring them back), and it can't sign in until enabled again. Invites the account made as an admin stop working.
- **Limits**: the storage quota and the largest file the user may upload. Enter sizes like `500 MB` or `2 GiB`; leave a field blank to use the site default. A quota of `0` means no limit. The per-user file size can only lower the site's limit.
- **Reset password**: a new set-password link, emailed when possible and otherwise shown once. It does not work while password sign-in is off.
- **Delete**: removes the account with all its items and secrets, files included. The confirmation names how many. The account is disabled first so nothing new arrives while it is emptied; secret files that can't be deleted at once are removed by the secrets sweeper.

The last active admin can't be demoted, disabled or deleted, by themselves or through the API. Make someone else an admin first.

## Invites

Create invite links with an optional email address, a role, a number of uses (1–1000) and an expiry (1, 7 or 30 days). The link is shown once; with SMTP configured and an address given, it is also emailed. The table lists every invite with who created it, uses, expiry and status; active invites can be revoked. Invite links don't work while sign-up is closed. The CLI can create invites with other lifetimes (see [accounts.md](accounts.md#invites)).

## Content

Every item on the site, newest first, with owner, kind, size, visibility, views, creation and expiry. Filter by owner username, kind, or text in the name, link or target. Deleting an item here removes it and its files and is audited as `moderation.delete`.

## Secrets

Metadata of every secret: owner (or `anonymous`), id, kind, mode (`e2e` or server-encrypted), state, views used and left, creation and expiry. Admins never see a secret's content, key or note. The list shows secrets that can still be opened; pick **all** for burned, expired and revoked ones too.

**Burn a secret** takes a full secret link or a bare 22-character id. The page removes the `#key` part of a pasted link before sending it, and the server drops it too if it arrives; the key is never logged or stored. Nothing burns without the confirmation dialog: Enter in the field opens it, and the server refuses a burn the dialog's button didn't send. Burning revokes the secret at once, stops downloads in progress, deletes its files and is audited as `moderation.burn`. People with the link then see that it was revoked.

## Settings

Every site setting, grouped by area (sign-in, uploads, items, secrets, media, stats, storage, site, and any others features add). Each field shows its key and default.

- Switches apply as soon as you flip them. Other fields are saved per group with **Save**; if any field in the group is refused, nothing in it is saved.
- Sizes are entered and shown in units (`1 GB`, `256 MiB`) and stored in bytes. Durations such as the secret expiry cap take `30d`, `12h` or `90m`. Lists take one entry per line. Settings without a dedicated control are edited as JSON.
- Values are checked by the same rules as `snap-server admin settings set`.
- Password sign-in can't be turned off unless SSO is configured, and open sign-up needs SMTP.
- **Send test email** checks mail delivery when SMTP is configured.

Changes made with the CLI reach a running server within a minute.

## Audit

The audit log, newest first, 50 entries a page, in UTC. Filter by action, actor username and a date range. Each entry has the time, the actor (with the API key id when one was used, `cli` for the admin CLI), the client IP, the action, its target and details.

## API

The same operations are available as JSON under `/api/v1/admin` for scripts; see [api.md](api.md#admin).
