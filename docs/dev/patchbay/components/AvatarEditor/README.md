The avatar editor lets someone upload a profile photo, crop it square, and save it.

## Markup

- The photo field: `div.pb-avfield[data-avatar-edit]` holding a `pb-avatar pb-avatar--xl` (with `data-avatar` and `data-name`), then `pb-avfield__body` with a `pb-avfield__title`, a `pb-avfield__hint` (`aria-live="polite"`, the accepted types and minimum size) and `pb-avfield__acts`.
- In the actions: `label.pb-btn` wrapping `input[type=file].pb-sr[data-avatar-pick]` (set `accept="image/png,image/jpeg,image/webp"` and `aria-describedby` to the hint), with the button text in a `[data-label]` span; then `button[data-avatar-remove]`.
- The crop dialog is built by the script the first time a photo is picked. Its words come from `pbStrings` (`avEdit`, `avSave`, `avKeys`, …).
- Options on the field:
  - `data-size` is the output side in pixels (default `512`).
  - `data-min` is the smallest side a photo may have (default `128`). Zoom also stops where the crop would hold fewer source pixels than this.
  - `data-max-mb` is the largest file accepted (default `8`).
  - `data-type` is the output type (default `image/webp`, which falls back to PNG where the browser can't encode WebP).
- `data-avatar-sync="selector"` names other avatars that show the same person, such as the one in the header. They change with the field.
- `pbAvatarEdit(el)` wires the field (included in `pbInit`). `el.pbAvatarOpen(file)` opens a `File` you already have.

## States

- No photo: the avatar shows initials, the button reads "Upload photo", and Remove is hidden. With a photo, the button reads "Change photo" and Remove shows.
- Rejected file: the dialog stays closed and the hint says why, in `down` with `status-down`: the wrong type, too large, too small, or unreadable (HEIC from older iPhones, say).
- Cropping: the photo sits on `night` behind a frame. The frame is a circle in soft and a square in hard, the same shape as the avatars it makes. Four live previews (64, 44, 32 and 24 px) sit beside it, and the footer shows the output size and the source size.
- Saving: Save shows "Saving…" and ignores clicks. On `pb:saved` the dialog closes, every synced avatar shows the new photo (keeping its presence dot), and a toast confirms. On `pb:error` the dialog stays open with the message in the footer, so nothing is lost.
- Removing: on `pb:saved` the avatars go back to initials, and a toast offers Undo.
- Drop zone: while a file is dragged over it, the field takes `data-state="over"`. Pasting an image works while focus is inside the field or the dialog.
- Under 560px the previews and controls stack under the photo. On touch screens its buttons are 44px.

## Keys

- The file input is the focus target. Its label shows the focus ring.
- When the dialog opens, focus moves to the crop area (a focusable group named "Crop area" and described by the key hint).
- In the crop area: arrows move the photo (Shift moves it further), `+` and `-` zoom, `R` rotates 90°, and `0` resets. The zoom slider is an ordinary range input with zoom-out and zoom-in buttons.
- Esc or Cancel closes the dialog without saving, and focus returns to the file input.

## Events

- `[data-avatar-edit]` fires `pb:avatar` with one of these:
  - `{action:'save', blob, file, width, height, crop}`. `crop` is `{x, y, size, rotate}` in pixels of the original after rotation, so a server can redo the crop from `file`.
  - `{action:'remove'}`.
  - `{action:'restore', url}` after Undo, with the photo as it was before.
- Settle each one on the same element. `pb:saved` can carry `detail.url`, the stored photo's URL; without it the avatars use a `blob:` URL of the new photo. `pb:error` carries `detail.message`. The kit doesn't upload or confirm anything itself.

## Do and don't

- Do: upload the blob, not the original file. The re-encoded photo drops camera metadata, including GPS. Keep `file` only if you want to redo the crop on the server.
- Do: save the photo on its own, outside a section form. A profile's other fields wait for Save, but the photo shouldn't.
- Do: state the accepted types and the minimum size in the hint before anyone picks a file.
- Don't: upscale small photos. Raise `data-min` instead of letting a 64px photo turn into a blurry 512px one.
- Under a strict CSP, allow `img-src blob:` (the kit's default policy already does).
