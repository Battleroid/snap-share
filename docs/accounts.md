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

An invite has a use limit, an expiry (1 day to 1 year), a role for the accounts it creates, and optionally an email address. With SMTP configured and an address given, the web form emails the link; the CLI only prints it. The link is shown once; only its hash is stored. Admins can revoke an invite. An invite also stops working when the admin who made it is disabled, demoted or deleted (invites from the CLI have no creator and are not affected).

The invitee opens the link, then either creates a local account or continues with SSO; the invite carries through the SSO round trip. If they keep the address the invite was made for, it counts as verified. If they enter another address, it is added once they confirm it by email.

## Email verification

Accounts from open sign-up must confirm their email address. Until then they can sign in, see a notice on **Settings › Profile** (with a resend button) and cannot create anything. Accounts from invites, the CLI or SSO never hold an unconfirmed address, so they are not limited.

Changing the address on **Settings › Profile** needs the current password (see [Confirming it's you](#confirming-its-you)) and sends a link to the new address; the old one stays until the link is used. The page answers the same whether or not another account uses the new address (then no mail goes out, and confirming fails), and it counts against the 3 verification emails per hour before anything else, so it can't be used to find out which addresses have accounts.

Opening a verification link changes nothing by itself: the page shows the account and the address and waits for **Confirm**. Mail scanners open links, and an address confirmed for an account its owner never made is how an account gets pre-hijacked.

## Links in email

Verify, reset and invite links look like `https://share.example.com/reset#t=<token>`. The token is in the URL fragment, which browsers never send, so it never appears in access logs. A small script on the page reads it, removes it from the address bar and history, and posts it to the server (for verify links, only after **Confirm**).

Tokens are 256-bit random values, stored as SHA-256, single use, and expire: verification 48 hours, password reset 1 hour (24 hours for links printed by the CLI), invites as set. A new reset or verification link replaces the previous one.

## Sessions

- The session cookie is `__Host-snap_session` (Secure, HttpOnly, SameSite=Lax). Each sign-in gets a new session id.
- A session ends after 30 days without use, and 90 days after sign-in regardless of use.
- **Settings › Security** lists sessions with their device and address. You can sign out one session or all of them.
- Changing your password ends every session, this one too, and gives this browser a new one, so a stolen copy of its cookie stops working. **Also revoke API keys** (checked by default) revokes your keys as well.
- A password reset signs out every session and revokes every API key: whoever had the password may have made keys with it.
- **Sign out everywhere** signs out every session, this one too, and revokes every API key.
- Disabling a user signs them out and revokes their API keys; enabling them again does not bring the keys back. Deleting a user removes everything.

## Two-factor authentication

A second factor makes a password alone not enough to sign in: after the password, snap asks for a passkey or security key, a code from an authenticator app, or a recovery code. A passkey can also sign in without a password. Set them up in **Settings › Security**.

### Authenticator app and recovery codes

- **Set up** an authenticator app after [confirming it's you](#confirming-its-you): snap shows a QR code and the setup key, and the app is on once you enter a code from it. Any app that follows RFC 6238 works (6 digits, 30 seconds, SHA-1). The secret is stored encrypted with a key derived from `SNAP_SECRET_KEY` ([rotating it](configuration.md#sign-in-and-sessions)).
- Codes are accepted up to 30 seconds early or late. Each code works once, and so does any older one.
- **Replace** sets up a new app. The old one keeps working until a code from the new one is confirmed.
- With your first passkey or authenticator app, snap shows **10 recovery codes**, once. Each works once in place of a passkey or a code from the app. **Make new codes** replaces them all. Settings shows how many are left.
- Adding the first passkey or authenticator app signs out your other sessions.
- Removing the last one turns two-factor off, and the recovery codes go with it. While `auth.require_2fa` covers you, that is refused.

### Signing in

After the password, `/login/2fa` asks for one of the second factors. The step expires after 5 minutes. It ends after 5 wrong codes, and wrong codes also count against 5 per 15 minutes per account, across sign-ins, so starting over gives no more guesses.

A password reset for an account with two-factor leads to the same step. Nothing changes until the factor is given: the password, sessions and API keys stay as they were, so someone with only the mailbox can't take over the account or lock its owner out. Admin set-password links work the same way.

SSO sign-ins skip the step: the identity provider owns 2FA there. That's why linking SSO needs the second factor too, and why `SNAP_OIDC_LINK_BY_EMAIL` never links into an account that has one.

### Requiring it

The admin setting `auth.require_2fa` (`off`, `admins` or `everyone`) makes the users it covers set up a passkey or an authenticator app at their next password sign-in, before they can do anything else. It applies to existing sessions at once. Until they set one up, their session reaches only **Settings › Security** and sign-out. Other pages send them there, and public pages treat them as signed out. SSO sign-ins leave 2FA to the identity provider. API keys keep working: they can't make sessions or links.

### Lost factors

An owner who lost their passkeys, authenticator app and recovery codes asks an admin. **Reset two-factor** on **Admin › Users** (it needs the admin's own password and second factor) or `snap-server admin reset-2fa --username U` removes them all and signs the user out everywhere. Their password alone then signs them in. The admin page can't reset the admin's own account; another admin or the CLI can.

### Security emails

With SMTP set up, snap emails the account's verified address when a passkey or authenticator app is added or removed, a recovery code is used (with how many are left), new recovery codes are made, an admin resets two-factor, or the password is reset. These go out whatever the notification settings, and link only to **Settings › Security**. The in-app inbox isn't used for them: whoever holds a stolen session could read and clear it.

## Confirming it's you

Changing the email address, setting or changing the password, linking and unlinking SSO, setting up or removing an authenticator app or passkey, making new recovery codes, and creating an API key with the `admin` scope all need proof that the account's owner is at the keyboard, not just someone with the session:

- Accounts with a password (and password sign-in on) enter the current password. Wrong passwords count against the same 5 per 15 minutes as sign-ins.
- With two-factor on, the same form also takes a second factor: a code from the authenticator app, a recovery code, or a passkey. Wrong codes count against the second step's budget. A recent sign-in doesn't stand in for either.
- Accounts without a password (SSO only) need a sign-in from the last 10 minutes, or a passkey. Otherwise the page asks to sign in with SSO again, then come back.

Keys with the `admin` scope can't be created through `POST /api/v1/keys` by a user with two-factor: that API has no form for the proof.

## Single sign-on

When SSO is configured, the sign-in page shows **Continue with Gitea** (the label comes from `SNAP_OIDC_NAME`). At sign-in snap looks for, in order:

1. An account already linked to this SSO account (matched by the provider's stable user id, not the username). Sign in.
2. Linking from **Settings › Security** while signed in, after [confirming it's you](#confirming-its-you) (the **Link** button posts the form; a plain link to `/auth/oidc/start?link=1` does nothing, so another site can't start a link): link to the current account.
3. With `SNAP_OIDC_LINK_BY_EMAIL=true`: a local account without a password whose verified email equals the provider's verified email (an account an admin created). Link and sign in. An account with a password or a second factor is never linked this way: snap refuses and says to sign in and link from Settings. Otherwise someone could register with another person's address, get it confirmed, and keep the password to the account that person's SSO sign-in then lands in. Off by default, because Gitea's `email_verified` means the account was activated, not that the person owns the mailbox.
4. With auto-provisioning on, or an invite: create an account named after the SSO username. If that name is taken, snap picks a free one (`ann-2`) and never merges into the existing account.
5. Otherwise: refuse, and say to ask for an invite or link the account from Settings.

`SNAP_OIDC_ALLOWED_GROUPS` limits SSO to members of those groups. When a linked account is refused because it left them, its sessions end (and, with a role mapping, it is demoted as below).

With `SNAP_OIDC_ADMIN_GROUPS` or `SNAP_OIDC_ADMIN_SUBS` set, roles come from the provider and are read at every SSO sign-in: admin if their id is in the subs list or they are in an admin group, otherwise user. Removing someone from the group demotes them at their next SSO sign-in, which also ends their sessions and revokes their API keys that hold the `admin` scope. The mapping never demotes the last active admin: it keeps the role and logs a warning. Since the provider is only asked at sign-in, an SSO-linked admin's session keeps admin rights for 24 hours after an SSO sign-in; after that (or after signing in with a password) the admin pages answer "Admins only" until they sign in with SSO again. API keys are not re-checked against the provider: to cut someone off at once, disable their account here. Without either variable, roles are left as admins set them.

A linked account can be unlinked from **Settings › Security** only if the account keeps another way in (a password with password sign-in on, or another linked account), and only after [confirming it's you](#confirming-its-you).

## Rate limits

Counted per client address unless the address is in `SNAP_RATELIMIT_EXEMPT`; limits run before any password is checked.

| Action | Limit |
|---|---|
| Sign in | 10 per minute per address, and 5 failures per 15 minutes per name typed (always applies) |
| Two-factor codes | Each attempt counts as a sign-in for the address; 5 wrong codes end the step; 5 wrong codes per 15 minutes per account, shared with confirming it's you (always applies) |
| Sign up, password reset, invite check | 5 per hour per address each |
| Email verification | 10 requests per hour per address (each link takes two: the check, then Confirm) |
| SSO start and callback | 20 per minute per address |
| Verification emails from Settings | 3 per hour per account |

The sign-in budget is per name as typed (case and surrounding spaces don't count): a username and the account's email address have a budget each. A shared budget would answer "too many attempts" for an address exactly when it belongs to a locked username, which tells anyone who owns which address. Each attempt takes from the budget before the password is checked and a successful one gives it back, so parallel guesses get no more than what is left; parallel sign-ins with the right password (several browsers at once) wait for each other for up to 10 seconds rather than being refused.

## Admin CLI

`snap-server admin` opens the database directly and is safe to run while the server runs. Settings changed this way reach a running server within a minute.

```
snap-server admin create-user --username U [--email E] [--admin] [--password-stdin]
snap-server admin set-role U admin|user
snap-server admin reset-password U
snap-server admin reset-2fa --username U
snap-server admin invite [--email E] [--uses N] [--expires 7d] [--admin]
snap-server admin settings list
snap-server admin settings get KEY
snap-server admin settings set KEY JSON
```

Values for `settings set` are JSON: `'"open"'`, `false`, `'"My share"'`.

## Audit log

These actions write `audit_log` rows: `auth.login`, `auth.login_failed`, `auth.logout_everywhere`, `user.create`, `user.register`, `user.password_change`, `user.password_reset`, `user.reset_link`, `user.email_change`, `user.role_change`, `user.status`, `invite.create`, `invite.revoke`, `settings.change`, `sso.link`, `sso.unlink`, `mfa.totp_enabled`, `mfa.totp_removed`, `mfa.recovery_regenerated`, `user.mfa_reset`, and `key.revoke_all` (keys revoked by a reset, a password change, sign out everywhere, disabling or an SSO demotion; `details.reason` says which).

`auth.login` has `details.method`: `password`, `sso`, `reset`, `passkey`, or with a second factor `password+totp`, `password+recovery`, `password+key` (and `reset+…` after a reset). A wrong second-factor code writes `auth.login_failed` with `details.method` `totp` or `recovery`. `reset-2fa` from the CLI has `details.via` `cli`.

## For developers

- `accounts.Service` is the request `auth.Authenticator`. An `Authorization` header is handed to the `BearerVerifier` set with `SetBearerVerifier` (API keys); until one is set every such header gets 401. Cookies are never consulted for those requests.
- `SetKeyRevoker` (the keys track's `keys.Store`: `RevokeAllForUser`, `RevokeAdminForUser`) ends a user's keys inside the accounts transaction on reset, password change, sign out everywhere, disabling and SSO demotion. Without one (the CLI), keys are left alone.
- Sessions record `auth_at` (the sign-in) and `sso_at` (the last SSO sign-in); `ReauthWindow` and `SSOAdminMaxAge` count from them.
- `auth.Principal.EmailVerified` is false only for self-registered accounts that have not confirmed their address. Features that create content check it.
- `accounts.Store` reads and writes users (`User`, `UserByUsername`, `PrincipalFor`, `CreateUser`, `SetRole`, `SetStatus`, …). `Service.CurrentUser(r)` loads the signed-in user.
- Settings keys: `auth.signup_mode`, `auth.password_login`, `auth.require_2fa` (`accounts.SignupMode`, `accounts.PasswordLogin`, `accounts.Require2FA`).
- Other packages that need confirm it's you use `Service.Proof(r)` for the form (the `security.proof` template) and `Service.ConfirmOwner(r)` to check it; the keys package does this for the `admin` scope. `Service.AdminResetMFA(r, id)` is the admin reset; `Service.ResetMFA` the CLI's.
- Tests: `testutil.NewServer(t, testutil.RealAuth())` uses real sessions; `testutil.WithSMTP()` turns mail features on with the fake mailer; `internal/auth/oidctest` is a stub OIDC provider (`testutil.OIDCClient(p.Client())`).
