# Local demo

`make demo-up` runs the current tree as a demo: snap behind Caddy with its own certificate authority, plus Mailpit for the email it sends. It is for trying changes in a browser, and with `make e2e`, `make shots` and `make review` against a running instance. Nothing in it is meant for production; [deployment.md](../deployment.md) covers that.

By default the demo listens on `127.0.0.1` only, so it is reachable from the machine it runs on and from nowhere else. Docker is the only requirement.

```sh
make demo-up        # build the image and start snap, Caddy and Mailpit
make demo-down      # stop it; state stays in .demo/
make demo-logs      # follow the snap log
```

| Variable | Default | Meaning |
|---|---|---|
| `DEMO_HOST` | `127.0.0.1` | The address the demo binds to (ports 8443 for the app, 8025 for Mailpit). |
| `DEMO_NAME` | `127-0-0-1.sslip.io` (from `DEMO_HOST`) | The demo's public name: `SNAP_PUBLIC_URL`, the Caddy site and the address `make e2e`, `shots` and `review` use. |

The app is at `https://$DEMO_NAME:8443`; `https://$DEMO_HOST:8443` redirects there. Mail is at `http://$DEMO_HOST:8025`.

## Trying it from another machine

Bind the demo to an address the other machine can reach, such as the host's LAN address:

```sh
make demo-up DEMO_HOST=<your LAN IP>
```

The name follows the address (`192-0-2-10.sslip.io` for `192.0.2.10`). Anyone on that network can then open the demo and its mail, so use it on a network you trust and stop it with `make demo-down` when you are done. Pass the same `DEMO_HOST` (and `DEMO_NAME`, if you set it) to `make e2e`, `shots` and `review`.

## Why a name, not the IP

Browsers refuse passkeys (WebAuthn) on an IP address: the relying party has to be a domain name. [sslip.io](https://sslip.io) answers any `a-b-c-d.sslip.io` with the address `a.b.c.d`, so the default name needs no setup.

Some routers and DNS filters drop public answers that point at private or loopback addresses (DNS rebinding protection). If the name doesn't resolve on your machine, use a name from your hosts file instead. For the local demo, `localhost` needs nothing more:

```sh
make demo-up DEMO_NAME=localhost
```

For a demo on a LAN address, add a name for it on the machine you browse from:

```sh
echo "192.0.2.10 snap.test" | sudo tee -a /etc/hosts
make demo-up DEMO_HOST=192.0.2.10 DEMO_NAME=snap.test
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
