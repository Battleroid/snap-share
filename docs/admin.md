# Admin

The admin area is at `/admin`. Admins get an **admin** group in the sidebar under their own pages: Overview, Users, Invites, Content, Secrets, Settings and Audit, plus Components (`/_kit`, the gallery of the page partials, for developers). Everything there is also logged in the audit log.

## Access

Only admins get in: a signed-in admin, or an API key with the `admin` scope (which only admins can create). Everyone else gets `403`. A key never sees set-password or invite links (they are only emailed) and can't make anyone an admin; see [api.md](api.md#admin).

Set `SNAP_ADMIN_CIDRS` (comma-separated addresses or CIDRs) to allow the admin pages and the admin API only from those networks, for example `SNAP_ADMIN_CIDRS=192.168.1.0/24`. Other pages are not affected. The address is the client address after `SNAP_TRUSTED_PROXIES` is applied.

Make the first admin with the CLI (see [accounts.md](accounts.md#creating-the-first-admin)) or through the SSO admin mapping.

## Overview

Tiles for active accounts (with the disabled count), the number of items, storage used (with the free space on the files disk), active secrets, and uploads over the last 30 days with a per-day line. Uploads are counted from items that still exist. Below them, the latest audit entries.

## Users

A table of accounts, searchable by username or email. The user cell holds the name with badges only for `admin` and `disabled` (a disabled row is muted; your own row says `you`), and the email with a verified or unverified mark. Sign-in shows icons for passkeys (with the count), the authenticator app and a linked SSO account; hover one for details, such as the recovery codes left. Then the item count, storage used (with a meter when the user has a quota), and the last sign-in. On a phone only the user cell and the row menu show; the rest is on the user's page. The row menu has Edit, Reset password, Disable or Enable, and Delete. Reset password and Disable ask first.

- **New user**: a username, an optional email address and a role. With password sign-in on, snap issues a link to set a password, valid once for 24 hours. With SMTP configured and an address given, the link is emailed; otherwise it is shown once on the page for you to pass on. An address you enter is treated as verified.
- **Role**: `user` or `admin`.
- **Disable**: the account is signed out everywhere, its API keys are revoked (enabling it again doesn't bring them back), and it can't sign in until enabled again. Invites the account made as an admin stop working.
- **Limits**: the storage quota and the largest file the user may upload. Enter sizes like `500 MB` or `2 GiB`; leave a field blank to use the site default. A quota of `0` means no limit. The per-user file size can only lower the site's limit.
- **Reset password**: a new set-password link, emailed when possible and otherwise shown once. It does not work while password sign-in is off. For a user with two-factor, the link still asks for their second factor.
- **Reset two-factor**: for a user who lost their passkeys, authenticator app and recovery codes. It removes all three and signs them out everywhere; their password alone then signs them in, and with `auth.require_2fa` they set up a new factor first. The dialog asks for your own password and, if you have one, your second factor. It is audited as `user.mfa_reset` and emailed to the user. Make sure it's really them asking: this is what someone who stole a password would ask for. It can't reset your own account: manage your factors in Settings, or ask another admin. There is no API for it; the CLI has `snap-server admin reset-2fa --username U`.
- **Delete**: removes the account with all its items and secrets, files included. The confirmation names how many. The account is disabled first so nothing new arrives while it is emptied; secret files that can't be deleted at once are removed by the secrets sweeper.

The last active admin can't be demoted, disabled or deleted, by themselves or through the API. Make someone else an admin first.

## Invites

Create invite links with an optional label (up to 80 characters, to tell invites apart), an optional email address, a role, a number of uses (1–1000) and an expiry (1, 7 or 30 days). The link is shown after creating; with SMTP configured and an address given, it is also emailed, and the notice says whether the email went out. Invite links don't work while sign-up is closed. The CLI can create invites with other lifetimes (see [accounts.md](accounts.md#invites)).

The table shows active invites; **all** adds the finished ones. Each row has who created it, uses, expiry and a status:

| Status | Means |
|---|---|
| `link` | Active, nothing emailed. |
| `sent` / `send failed` | Active; the email went out, or failed (hover for why). |
| `partly used` | Some of its uses are taken. |
| `unusable` | Its creator is no longer an active admin, so it doesn't work. |
| `used`, `expired`, `revoked` | Finished. |

The row menu of an active invite has **Copy link**, which shows the link again in a dialog, and **Revoke…**. Copying is audited (`invite.link`) and only works from a browser session; API keys never get it. Invites made before snap kept links say "link not stored". A finished invite can be removed (**Remove…**, audited as `invite.remove`); finished invites are also deleted on their own 30 days after they expire or are revoked.

## Content

Every item on the site, newest first, with owner, kind, size, views, creation and expiry. The name is marked `private` or `password` when it is; unlisted, the default, is not marked. Filter by owner username, kind, or text in the name, link or target. Deleting an item here removes it and its files and is audited as `moderation.delete`.

## Secrets

Metadata of every secret: owner (or `anonymous`), id, kind and mode in one cell (`text · e2e`, `file · server-encrypted`, with a lock mark when a passphrase is set), state, views used and left, creation and expiry. Admins never see a secret's content, key or note. The list shows secrets that can still be opened; pick **all** for burned, expired and revoked ones too.

**Burn a secret** takes a full secret link or a bare 22-character id. The page removes the `#key` part of a pasted link before sending it, and the server drops it too if it arrives; the key is never logged or stored. Nothing burns without the confirmation dialog: Enter in the field opens it, and the server refuses a burn the dialog's button didn't send. Burning revokes the secret at once, stops downloads in progress, deletes its files and is audited as `moderation.burn`. People with the link then see that it was revoked.

## Settings

Every site setting, grouped by area (sign-in, uploads, items, secrets, media, stats, storage, email sending, notifications, metrics, site, and any others features add). Each field shows its key and default.

- Switches apply as soon as you flip them. Other fields are saved per group with **Save**; if any field in the group is refused, nothing in it is saved.
- Sizes are entered and shown in units (`1 GB`, `256 MiB`) and stored in bytes. Durations such as the secret expiry cap take `30d`, `12h` or `90m`. Lists take one entry per line. Settings without a dedicated control are edited as JSON.
- Values are checked by the same rules as `snap-server admin settings set`.
- Password sign-in can't be turned off unless SSO is configured, and open sign-up needs SMTP.
- **Send test email** checks mail delivery when SMTP is configured.

Changes made with the CLI reach a running server within a minute.

## Audit

The audit log, newest first, 50 entries a page, in UTC. Filter by action, actor username and a date range. Each entry has the time, the actor (with the API key id when one was used, `cli` for the admin CLI), the client IP, the action, its target and details. The table never scrolls sideways: actor, IP, target and details hide on a phone, and the details cell is cut to one line (hover it for the full text). An entry's expanded row shows every field, and the details as stored (JSON). To also send entries to a log shipper, set `SNAP_AUDIT_LOG` ([observability.md](observability.md#audit-stream)).

## API

The same operations are available as JSON under `/api/v1/admin` for scripts; see [api.md](api.md#admin).
