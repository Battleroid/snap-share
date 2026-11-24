# ShareX

snap generates ShareX custom uploader files (`.sxcu`) for screenshots and files, text, and short links.

## Set up

1. Sign in and open **Settings › Integrations**.
2. Click a download. Each one creates a new API key and puts it in the file:

   | Download | ShareX destination | Creates |
   |---|---|---|
   | **Download uploader** | Image uploader, File uploader | files, via `POST /api/v1/upload` |
   | **Download text uploader** | Text uploader | pastes, via `POST /api/v1/pastes` |
   | **Download URL shortener** | URL shortener | short links, via `POST /api/v1/links` |

3. Open the `.sxcu` file (double-click it, or in ShareX: **Destinations › Custom uploader settings › Import › From file**). ShareX asks whether to make it the active uploader for its destination types; say yes.
4. To choose it later: **Destinations › Image uploader / File uploader / Text uploader / URL shortener › Custom … uploader**, then pick it under **Destinations › Custom uploader settings**.

Uploads use your defaults from **Settings › Defaults** (naming, visibility, expiry), unless the uploader sets options.

## Options per uploader

Any of the [options](api.md#options) (`expires`, `expiry_action`, `visibility`, `password`, `naming`, `length`, `slug`, `title`, and `name` for the file name) can be set in ShareX under **Destinations › Custom uploader settings**, or by editing the `.sxcu` before opening it. **Settings › Integrations › Upload options** shows the same examples.

- **Uploader (files):** add them as body arguments (`Arguments`). These are sent as multipart form fields.

  ```json
  "Arguments": {"expires": "7d", "visibility": "private", "naming": "words"}
  ```

- **Text uploader and URL shortener:** these send a JSON body, so add the options as keys in `Data`: `{"content":"{input}","expires":"1d"}`, `{"url":"{input}","slug":"docs"}`.
- **URL query parameters** (`Parameters`) are ignored by all three endpoints.

To be asked on each upload, use ShareX's `{select:a|b|c}` (buttons) and `{inputbox:title}` (a text box) as values:

```json
"Arguments": {
  "expires": "{select:1d|7d|30d|never}",
  "visibility": "{select:unlisted|private}",
  "password": "{inputbox:Password, or leave blank}"
}
```

ShareX runs these only in body arguments. In a JSON `Data` body it fills in `{input}` and `{filename}` and nothing else. To get prompts on the text uploader or URL shortener, set `"Body": "FormURLEncoded"` and move the content into the arguments, as `"content": "{input}"` or `"url": "{input}"`. snap accepts form bodies on both endpoints. A blank answer uses your default.

A password written into the file is stored in plain text next to the key; prefer `{inputbox:…}`. A key with restrictions overrides these options: a forced visibility replaces the one asked for, and a maximum expiry shortens longer ones.

## What each upload returns

ShareX reads these from the JSON response:

| ShareX field | Value | Notes |
|---|---|---|
| `URL` | `{json:url}` | the share page, or the raw file when "Copy direct link" is on |
| `ThumbnailURL` | `{json:thumbnail_url}` | files only; ready at once for images |
| `DeletionURL` | `{json:deletion_url}` | `https://…/x#<token>`; opening it asks to confirm, then deletes the item without signing in |
| `ErrorMessage` | `{json:error}` | shown by ShareX when an upload fails |

## The keys

- The keys are named after the download and the date ("ShareX uploader 2026-11-22") and listed under **Settings › API keys**.
- They have only the `items:write` scope: they can create items but not list, read or delete them. They can change (`PATCH`) only the items that key created; every other item of yours answers `404` to them, and on share pages and raw URLs they are an ordinary visitor (your private items are `404` to them). Deletion goes through each item's deletion URL.
- They never expire. Revoke a key there to disconnect that ShareX install; downloading again makes a new key and leaves the old one working until you revoke it.
- The `.sxcu` file contains the key. Keep it like a password.

To limit what a ShareX key can do (force private items, cap expiry or size, allow only your home network), create a key yourself at **Settings › API keys** with `items:write` and the restrictions you want, then replace the token after `Bearer ` in the file's `Authorization` header (or in ShareX's custom uploader settings).

## File format

For reference, the uploader file looks like this (ShareX 13.7 or later; older versions use a different syntax):

```json
{
  "Version": "15.0.0",
  "Name": "snap files (share.example.com)",
  "DestinationType": "ImageUploader, FileUploader",
  "RequestMethod": "POST",
  "RequestURL": "https://share.example.com/api/v1/upload",
  "Headers": {"Authorization": "Bearer snap_…"},
  "Body": "MultipartFormData",
  "FileFormName": "file",
  "URL": "{json:url}",
  "ThumbnailURL": "{json:thumbnail_url}",
  "DeletionURL": "{json:deletion_url}",
  "ErrorMessage": "{json:error}"
}
```

The text uploader sends `"Body": "JSON"` with `"Data": "{\"content\":\"{input}\"}"` (ShareX escapes the text for JSON), and the URL shortener `"Data": "{\"url\":\"{input}\"}"`.

## Troubleshooting

| ShareX shows | Cause |
|---|---|
| `Invalid credentials.` | the key was revoked or mistyped; download a new file |
| `This key is not allowed from this address.` | the key has allowed networks and you are outside them |
| `Verify your email address first.` | confirm your email at **Settings › Profile** |
| `The file is over the … limit.` / `That would put you over your storage quota.` | size limit or quota |
| `This file type is not allowed.` | the admin blocked the type or extension |
