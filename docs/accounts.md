# Accounts

How people get accounts, sign in and stay signed in. SSO setup with Gitea is in [gitea-sso.md](gitea-sso.md); environment variables are in [configuration.md](configuration.md).

## Creating the first admin

There is no "first visitor becomes admin". Create one with the CLI inside the container:

```sh
docker compose exec snap snap-server admin create-user --username alice --email alice@example.com --admin
```

Without `--password-stdin` it prints a one-time link to set the password (valid 24 hours). To set the password directly:

```sh
printf '%s\n' 'a long password' | docker compose exec -T snap snap-server admin create-user --username alice --admin --password-stdin
```

With SSO you can instead make people admins through `SNAP_OIDC_ADMIN_GROUPS` or `SNAP_OIDC_ADMIN_SUBS`.

## Sign-up modes

Set on **Admin › Settings** or with `snap-server admin settings set auth.signup_mode '"invite"'`.

| Mode | Who can create an account |
|---|---|
| `closed` | Nobody. Admins create accounts on **Admin › Users** or with the CLI. Invite links stop working. |
| `invite` (default) | People with an invite link. |
| `open` | Anyone, with email verification. Needs SMTP; without it the site behaves as `invite`. |

SSO auto-provisioning (`SNAP_OIDC_AUTO_PROVISION`, on by default) is separate from the mode: anyone your identity provider lets through gets an account, because the provider already decides who has an account there. Turn it off to require an invite for SSO users too.

**Password sign-in** (`auth.password_login`, on by default) can be turned off when SSO is configured. Then only SSO works, and local sign-up and password resets are off.

## Invites

Admins create invites on **Admin › Invites** or with the CLI:

```sh
snap-server admin invite --email bob@example.com --uses 1 --expires 7d
```

An invite has a use limit, an expiry (1 day to 1 year), a role for the accounts it creates, and optionally an email address. With SMTP configured and an address given, the web form emails the link; the CLI only prints it. The link is shown once; only its hash is stored. Admins can revoke an invite.

The invitee opens the link, then either creates a local account or continues with SSO; the invite carries through the SSO round trip. If they keep the address the invite was made for, it counts as verified. If they enter another address, it is added once they confirm it by email.

## Email verification

Accounts from open sign-up must confirm their email address. Until then they can sign in, see a notice on **Settings › Profile** (with a resend button) and cannot create anything. Accounts from invites, the CLI or SSO never hold an unconfirmed address, so they are not limited.

Changing the address on **Settings › Profile** sends a link to the new address; the old one stays until the link is used.

## Links in email

Verify, reset and invite links look like `https://share.example.com/reset#t=<token>`. The token is in the URL fragment, which browsers never send, so it never appears in access logs. A small script on the page reads it, removes it from the address bar and history, and posts it to the server.

Tokens are 256-bit random values, stored as SHA-256, single use, and expire: verification 48 hours, password reset 1 hour (24 hours for links printed by the CLI), invites as set. A new reset or verification link replaces the previous one.

## Sessions

- The session cookie is `__Host-snap_session` (Secure, HttpOnly, SameSite=Lax). Each sign-in gets a new session id.
- A session ends after 30 days without use, and 90 days after sign-in regardless of use.
- **Settings › Security** lists sessions with their device and address. You can sign out one session or all of them.
- Changing your password signs out every other session. A password reset signs out all of them.
- Disabling or deleting a user ends their access on the next request.

## Single sign-on

When SSO is configured, the sign-in page shows **Continue with Gitea** (the label comes from `SNAP_OIDC_NAME`). At sign-in snap looks for, in order:

1. An account already linked to this SSO account (matched by the provider's stable user id, not the username). Sign in.
2. Linking from **Settings › Security** while signed in: link to the current account.
3. With `SNAP_OIDC_LINK_BY_EMAIL=true`: a local account whose verified email equals the provider's verified email. Link and sign in. Off by default, because Gitea's `email_verified` means the account was activated, not that the person owns the mailbox.
4. With auto-provisioning on, or an invite: create an account named after the SSO username. If that name is taken, snap picks a free one (`ann-2`) and never merges into the existing account.
5. Otherwise: refuse, and say to ask for an invite or link the account from Settings.

`SNAP_OIDC_ALLOWED_GROUPS` limits SSO to members of those groups. With `SNAP_OIDC_ADMIN_GROUPS` or `SNAP_OIDC_ADMIN_SUBS` set, an SSO user's role is recomputed at every SSO sign-in: admin if their id is in the subs list or they are in an admin group, otherwise user (so removing someone from the group demotes them at their next sign-in). Without either variable, roles are left as admins set them.

A linked account can be unlinked from **Settings › Security** only if the account keeps another way in (a password with password sign-in on, or another linked account).

## Rate limits

Counted per client address unless the address is in `SNAP_RATELIMIT_EXEMPT`; limits run before any password is checked.

| Action | Limit |
|---|---|
| Sign in | 10 per minute per address, and 5 failures per 15 minutes per account (always applies) |
| Sign up, password reset, email verification, invite check | 5 per hour per address each |
| SSO start and callback | 20 per minute per address |
| Verification emails from Settings | 3 per hour per account |

## Admin CLI

`snap-server admin` opens the database directly and is safe to run while the server runs. Settings changed this way reach a running server within a minute.

```
snap-server admin create-user --username U [--email E] [--admin] [--password-stdin]
snap-server admin set-role U admin|user
snap-server admin reset-password U
snap-server admin invite [--email E] [--uses N] [--expires 7d] [--admin]
snap-server admin settings list
snap-server admin settings get KEY
snap-server admin settings set KEY JSON
```

Values for `settings set` are JSON: `'"open"'`, `false`, `'"My share"'`.

## Audit log

These actions write `audit_log` rows: `auth.login`, `auth.login_failed`, `auth.logout_everywhere`, `user.create`, `user.register`, `user.password_change`, `user.password_reset`, `user.reset_link`, `user.email_change`, `user.role_change`, `user.status`, `invite.create`, `invite.revoke`, `settings.change`, `sso.link`, `sso.unlink`.

## For developers

- `accounts.Service` is the request `auth.Authenticator`. An `Authorization` header is handed to the `BearerVerifier` set with `SetBearerVerifier` (API keys); until one is set every such header gets 401. Cookies are never consulted for those requests.
- `auth.Principal.EmailVerified` is false only for self-registered accounts that have not confirmed their address. Features that create content check it.
- `accounts.Store` reads and writes users (`User`, `UserByUsername`, `PrincipalFor`, `CreateUser`, `SetRole`, `SetStatus`, …). `Service.CurrentUser(r)` loads the signed-in user.
- Settings keys: `auth.signup_mode`, `auth.password_login` (`accounts.SignupMode`, `accounts.PasswordLogin`).
- Tests: `testutil.NewServer(t, testutil.RealAuth())` uses real sessions; `testutil.WithSMTP()` turns mail features on with the fake mailer; `internal/auth/oidctest` is a stub OIDC provider (`testutil.OIDCClient(p.Client())`).
