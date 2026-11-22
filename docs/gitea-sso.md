# Sign-in with Gitea

snap uses Gitea as an OpenID Connect provider. Users click **Continue with Gitea**, approve once in Gitea, and come back signed in. How accounts are matched and created is in [accounts.md](accounts.md#single-sign-on).

## 1. Create the OAuth2 application in Gitea

Either kind works:

- **For the whole Gitea instance** (recommended, not tied to one person's account): sign in as a Gitea admin, open **Site Administration › Integrations › Applications**.
- **For your user**: open **Settings › Applications**, section **Manage OAuth2 Applications**.

Fill in:

| Field | Value |
|---|---|
| Application name | `snap` (shown to users on Gitea's consent screen) |
| Redirect URIs | `https://<snap host>/auth/oidc/callback`, e.g. `https://share.example.com/auth/oidc/callback` |
| Confidential client | checked |

Click **Create application**. Gitea shows the **Client ID** and the **Client Secret**. Copy the secret now; Gitea does not show it again (you can regenerate it).

The redirect URI must match `SNAP_PUBLIC_URL` exactly: same scheme, host and port, path `/auth/oidc/callback`, no trailing slash.

## 2. Configure snap

```sh
SNAP_OIDC_ISSUER=https://git.example.com          # Gitea's ROOT_URL
SNAP_OIDC_CLIENT_ID=<client id>
SNAP_OIDC_CLIENT_SECRET_FILE=/run/secrets/snap_oidc_client_secret   # or SNAP_OIDC_CLIENT_SECRET=...
SNAP_OIDC_NAME=Gitea                               # button label
```

Optional:

```sh
SNAP_OIDC_AUTO_PROVISION=true        # create accounts for new Gitea users (default true)
SNAP_OIDC_ALLOWED_GROUPS=family      # only these Gitea orgs/teams may sign in
SNAP_OIDC_ADMIN_GROUPS=family:owners # these orgs/teams are snap admins
SNAP_OIDC_ADMIN_SUBS=1               # these Gitea user ids are snap admins
SNAP_OIDC_LINK_BY_EMAIL=false        # see accounts.md before turning this on
```

Restart snap. It fetches `https://git.example.com/.well-known/openid-configuration` on the first sign-in, not at startup, so snap starts even when Gitea is down; the button answers "Gitea is not reachable" until it is back. Gitea writes its issuer with a trailing slash; either spelling works in `SNAP_OIDC_ISSUER`.

snap asks for the scopes `openid profile email groups` and uses PKCE (S256).

## Finding a Gitea user's numeric id

`SNAP_OIDC_ADMIN_SUBS` takes Gitea user ids, which stay the same when someone renames their account. To find one:

- **Site Administration › User Accounts** (under **Identity & Access** in newer Gitea) lists users with their ID.
- Or ask the API: `curl -s https://git.example.com/api/v1/users/alice | jq .id`

## Groups

Gitea sends a `groups` claim listing:

- each organization the user belongs to, by name: `family`
- each team, as `org:team` with the team name in lower case: `family:owners`

Use those strings in `SNAP_OIDC_ALLOWED_GROUPS` and `SNAP_OIDC_ADMIN_GROUPS` (comma separated). Gitea may send groups only in its userinfo response; snap fetches userinfo when the ID token has no `groups` claim.

With `SNAP_OIDC_ADMIN_GROUPS` or `SNAP_OIDC_ADMIN_SUBS` set, snap recomputes an SSO user's role at every SSO sign-in, so removing someone from the admin team demotes them the next time they sign in. To demote them right away, also run `snap-server admin set-role <name> user`.

## Existing local accounts

A Gitea sign-in never takes over a local account with the same username. To use Gitea for an existing account, sign in with its password and click **Link Gitea account** on **Settings › Security**.

## Troubleshooting

| Message | Cause |
|---|---|
| "Gitea is not reachable" | snap cannot fetch the discovery document. Check `SNAP_OIDC_ISSUER` and that the snap container can reach Gitea (DNS, hairpin NAT). |
| Gitea says the redirect URI is invalid | The URI in the Gitea application differs from `SNAP_PUBLIC_URL` + `/auth/oidc/callback`. |
| "Sign-in with Gitea failed" | Code exchange or token check failed; the server log has the reason (wrong client secret, clock skew). |
| "expired or was started in another tab" | The sign-in took over 10 minutes, or cookies are blocked for the snap host. |
| "not in a group that may use this site" | The user is in none of `SNAP_OIDC_ALLOWED_GROUPS`. |
| "No account here uses this Gitea account" | Auto-provisioning is off and the user has no invite. |
