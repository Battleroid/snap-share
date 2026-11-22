# Configuration

snap reads `SNAP_*` environment variables at startup. Secrets can come from a file instead: set `SNAP_<NAME>_FILE` to its path (not both). Everything else is a site setting stored in the database, changed on **Admin → Settings** or with `snap-server admin settings set`.

<!-- Sections below are owned by the tracks that add them. -->

## Sign-in and sessions

| Variable | Default | Meaning |
|---|---|---|
| `SNAP_SECRET_KEY` / `_FILE` | required | At least 32 bytes. Signs the SSO state cookie (and other signed values). Generate with `openssl rand -base64 48`. |
| `SNAP_SECRET_KEY_PREVIOUS` / `_FILE` | — | The old key during a rotation; still accepted when checking signatures. |
| `SNAP_RATELIMIT_EXEMPT` | — | Addresses or CIDRs exempt from per-address rate limits (sign-in, sign-up, resets). Per-account sign-in limits still apply. |

Session cookies are `Secure` when `SNAP_PUBLIC_URL` is https (and then named `__Host-snap_session`). Serve snap over https outside local testing.

## SSO (OIDC)

SSO is on when `SNAP_OIDC_ISSUER` is set. Setup with Gitea: [gitea-sso.md](gitea-sso.md).

| Variable | Default | Meaning |
|---|---|---|
| `SNAP_OIDC_ISSUER` | — | Issuer URL (https). For Gitea, its `ROOT_URL`. |
| `SNAP_OIDC_CLIENT_ID` | — | Required with the issuer. |
| `SNAP_OIDC_CLIENT_SECRET` / `_FILE` | — | Required with the issuer. |
| `SNAP_OIDC_NAME` | `SSO` | Button label: "Continue with Gitea". |
| `SNAP_OIDC_AUTO_PROVISION` | `true` | Create accounts for new SSO users regardless of the sign-up mode. When false, new SSO users need an invite. |
| `SNAP_OIDC_ALLOWED_GROUPS` | — | Comma-separated groups; when set, only their members may sign in with SSO. |
| `SNAP_OIDC_ADMIN_GROUPS` | — | Comma-separated groups whose members are admins. |
| `SNAP_OIDC_ADMIN_SUBS` | — | Comma-separated provider user ids (`sub`) that are admins. |
| `SNAP_OIDC_LINK_BY_EMAIL` | `false` | Link an SSO sign-in to a local account with the same verified email. |

When `SNAP_OIDC_ADMIN_GROUPS` or `SNAP_OIDC_ADMIN_SUBS` is set, SSO users' roles follow them at every SSO sign-in, including demotion. When neither is set, roles are left alone.

The callback URL is `SNAP_PUBLIC_URL` + `/auth/oidc/callback`.

## Email (SMTP)

Mail is on when `SNAP_SMTP_HOST` is set. Without it there are no invite emails, verification or password reset emails, and open sign-up is unavailable; invite links and CLI-printed links still work.

| Variable | Default | Meaning |
|---|---|---|
| `SNAP_SMTP_HOST` | — | SMTP server host. |
| `SNAP_SMTP_PORT` | `465` | Usually 465 for `implicit`, 587 for `starttls`. |
| `SNAP_SMTP_TLS` | `implicit` | `implicit` (TLS from the start), `starttls` (upgrade required; refuses servers without it), `none` (plain text, only for a local relay such as Mailpit). |
| `SNAP_SMTP_USER` | — | Login name; no authentication when empty. |
| `SNAP_SMTP_PASSWORD` / `_FILE` | — | Password or app password. |
| `SNAP_SMTP_FROM` | — | Required with the host, e.g. `snap <snap@example.com>`. |

Admins can check delivery with **Send test email** on **Admin → Settings**. Message bodies and links are never logged.

Fastmail example:

```sh
SNAP_SMTP_HOST=smtp.fastmail.com
SNAP_SMTP_PORT=465
SNAP_SMTP_TLS=implicit
SNAP_SMTP_USER=you@fastmail.com
SNAP_SMTP_PASSWORD_FILE=/run/secrets/snap_smtp_password   # an app password
SNAP_SMTP_FROM=snap <snap@example.com>
```

## Site settings for accounts

| Key | Default | Meaning |
|---|---|---|
| `site.name` | `"snap"` | Name in the header, page titles and emails. |
| `auth.signup_mode` | `"invite"` | `"closed"`, `"invite"` or `"open"`. See [accounts.md](accounts.md#sign-up-modes). |
| `auth.password_login` | `true` | Allow password sign-in. Can be turned off only when SSO is configured. |
