QR codes let a phone open a share link or join a network without typing.

- Markup: `figure.pb-qr[data-qr="text"]` with an empty `div.pb-qr__code` and a `figcaption.pb-qr__caption`; `pbQRCode(el)` draws it. Size: `data-size="sm"` (132px), `"md"` (148px) or `"lg"` (220px), default 176px; `--qr-size` in a stylesheet sets any other size.
- The encoder is built in (`pbQR(text)`, `pbQRSvg(text)`): byte mode, error correction M, up to 213 bytes. No network or library.
- The code is always dark on white with a quiet zone, in both themes, so every camera reads it.
- Always show the link or name next to it and give a copy button. Wi-Fi codes use `WIFI:T:WPA;S:name;P:password;;`.
- Inside a popover or menu use `pb-qr--bare`; the popover is the frame. It drops the card (border, radius, padding, surface) and keeps only the white quiet zone, still dark on white in the dark theme so it scans, with the caption tight under it.
