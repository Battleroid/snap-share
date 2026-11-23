# LAN demo

`make demo-up` runs the current tree as a demo on the LAN: snap behind Caddy with its own certificate authority, plus Mailpit for the email it sends. It is for trying changes from another machine. Nothing in it is meant for production, which runs at `snap.example.com` with a Let's Encrypt certificate ([homelab.md](../homelab.md)).

| Variable | Default | Meaning |
|---|---|---|
| `DEMO_HOST` | `203.0.113.5` | The address the demo binds to (ports 8443 for the app, 8025 for Mailpit). |
| `DEMO_NAME` | `203-0-113-5.sslip.io` (from `DEMO_HOST`) | The demo's public name: `SNAP_PUBLIC_URL`, the Caddy site and the address `make e2e`, `shots` and `review` use. |

The app is at `https://$DEMO_NAME:8443`; `https://$DEMO_HOST:8443` redirects there. Mail is at `http://$DEMO_HOST:8025`. `make demo-down` stops it and keeps its state in `.demo/`.

## Why a name, not the IP

Browsers refuse passkeys (WebAuthn) on an IP address: the relying party has to be a domain name. [sslip.io](https://sslip.io) answers any `a-b-c-d.sslip.io` with the address `a.b.c.d`, so the default name needs no setup.

Some routers and DNS filters drop public answers that point at private addresses (DNS rebinding protection). If the name doesn't resolve on your machine, use a hosts-file name instead:

```sh
echo "203.0.113.5 snap.lan" | sudo tee -a /etc/hosts   # on the machine you browse from
make demo-up DEMO_NAME=snap.lan
```

A record in a local DNS server works the same way.

## Trusting the demo certificate

Caddy signs the demo's certificate with its own authority. Chromium-based browsers refuse passkeys on a page with a certificate error, so install the authority on the machine you browse from:

- The certificate is `.demo/caddy/data/caddy/pki/authorities/local/root.crt` (created by the first `make demo-up`).
- Firefox: Settings › Privacy & Security › Certificates › View Certificates › Authorities › Import.
- Chromium and Chrome on Linux: Settings › Privacy and security › Security › Manage certificates › Authorities › Import, or `certutil -d sql:$HOME/.pki/nssdb -A -t C,, -n snap-demo -i root.crt`.
- macOS: open it in Keychain Access, then set it to Always Trust.

Remove it when you're done with the demo.

## Passkeys on the demo

A passkey belongs to the name it was made on. Passkeys registered on the demo work only on that `DEMO_NAME`, and changing the name makes them unusable (sign in with the password and a code instead, or reset two-factor with `snap-server admin reset-2fa`).
