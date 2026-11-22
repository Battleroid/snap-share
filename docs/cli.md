# Command line: `sp`

`sp` uploads files, creates pastes and short links, and lists and deletes your items from a terminal.

## Install

Every snap server serves builds for Linux (amd64, arm64), macOS (arm64) and Windows (amd64) at `/dl/snap-cli-<os>-<arch>`. **Settings › Integrations** lists the ones the server has, with an install line:

```sh
curl -fLo sp https://share.example.com/dl/snap-cli-linux-amd64 && chmod +x sp
sudo mv sp /usr/local/bin/        # optional
```

On Windows the file is `snap-cli-windows-amd64.exe`.

Server operators: the files come from `SNAP_CLI_DIR` (default `/usr/share/snap/cli`, where the image puts them). Only names of the form `snap-cli-<os>-<arch>` (plus `.exe` for Windows) are served.

## Log in

1. Create a key at **Settings › API keys** in the browser. Keys are only made there; `sp` cannot create them. The default scopes (`items:read`, `items:write`, `items:delete`) cover every command; add `account:read` for `sp whoami`.
2. Run:

   ```sh
   sp login https://share.example.com
   ```

   It asks for the key, checks it, and saves the URL and key to the config file with mode `0600`:

   | System | Config file |
   |---|---|
   | Linux | `~/.config/snap/config.json` (or `$XDG_CONFIG_HOME/snap/config.json`) |
   | macOS | `~/Library/Application Support/snap/config.json` |
   | Windows | `%AppData%\snap\config.json` |

   When stdin is not a terminal, the key (and the URL, if not given) is read from it without prompts: `sp login https://share.example.com < key.txt`.

For scripts and CI, set `SNAP_URL` and `SNAP_KEY` instead; they override the config file.

## Commands

Every command prints results to stdout (one URL per line for uploads) and errors to stderr, and exits non-zero on failure. Flags may come before or after the arguments. Expiries are `never`, `1h`, `1d`, `7d`, `30d`, `90d` or an RFC 3339 time; anything not set uses your defaults from **Settings › Defaults**.

### `sp up <files…>`

Uploads files and prints one URL per file. Files are streamed, with progress on stderr when it is a terminal.

| Flag | Meaning |
|---|---|
| `--expire 7d` | expiry |
| `--private` | only you can open it |
| `--password-file f` | visitors must enter the password in file `f` (first line) |
| `--name random\|words\|original` | naming scheme |
| `--slug name` | a custom name (one file only) |

```sh
sp up screenshot.png
sp up *.jpg --expire 30d --name words
```

If some files fail, the others are still uploaded and the command exits with status 1.

### `sp paste [file|-]`

Creates a paste from a file, or from stdin (`-` or no argument).

| Flag | Meaning |
|---|---|
| `--lang go` | language for highlighting (default: from the name) |
| `--name main.go` | file name shown with the paste (default: the file's name) |
| `--expire 1d` | expiry |

```sh
sp paste main.go
kubectl logs api-7f9c | sp paste --name api.log --expire 1d
```

Pastes are UTF-8 text up to 16 MB; use `sp up` for anything else.

### `sp short <url>`

Creates a short link.

| Flag | Meaning |
|---|---|
| `--slug docs` | a custom name |
| `--expire 30d` | expiry |

### `sp ls`

Lists your newest items: id, name, kind, size, created, expires.

| Flag | Meaning |
|---|---|
| `--limit 20` | how many (1–200) |

### `sp rm <slug|url|id…>`

Deletes items. Each argument is a name (`aB3dE9xQ`), a link on your server (`https://share.example.com/aB3dE9xQ.png`), or a numeric id from `sp ls`. Use the link for an item whose name is all digits.

### `sp whoami`

Shows the account, the key's scopes and expiry, and storage use. Needs a key with `account:read`.

### `sp login [url]`, `sp version`

See [Log in](#log-in).

## Exit codes

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | the command failed (the message says why: the server's error, a missing file, a network problem) |
| `2` | unknown command |

## What the key can do

The key's scopes and restrictions apply to everything `sp` does. A key without `items:delete` cannot `sp rm` ("This needs the items:delete permission."); a key that forces private visibility makes every upload private. See [api.md](api.md#key-restrictions).
