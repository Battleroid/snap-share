QR codes let a phone open a share link or join a network without typing.

## Markup
- `figure.pb-qr[data-qr="text"]` with an empty `div.pb-qr__code` and a `figcaption.pb-qr__caption`; `pbQRCode(el)` (the `qr` hook) draws it as inline SVG. Size: `data-size="sm"` (132px), `"md"` (148px) or `"lg"` (220px), default 176px; `--qr-size` sets any other size, through a class or `data-vars="--qr-size:200px"`.
- The encoder is built in (`pbQR(text)`, `pbQRSvg(text)`): byte mode, error correction M, up to 213 bytes. No network or library.
- Wi-Fi codes use `WIFI:T:WPA;S:name;P:password;;`.

## States
- The code is always dark on white with a quiet zone, in both themes, so every camera reads it.
- Text longer than the encoder takes shows a "too long" message in place of the code.
- Inside a popover or menu use `pb-qr--bare`; the popover is the frame. It drops the card (border, radius, padding, surface) and keeps only the white quiet zone, still dark on white in the dark theme so it scans, with the caption tight under it.

## Keys
- Not interactive. The SVG carries an `aria-label` ("QR code for …"); the caption gives the same text visibly.

## Events
None; the component fires no events of its own.

## Do and don't
- Do: always show the link or name next to the code, and give a copy button.
- Don't: invert the code or tint it; keep it dark on white.
- Don't: encode more than 213 bytes.
