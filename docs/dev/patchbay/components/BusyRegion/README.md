A region whose content is being replaced.

- CSS: `[aria-busy="true"]` dims its children, blocks the pointer and shows a spinner, all after 300 ms so a fast swap never flashes. The inbox panel and tree items keep their own loading look; `data-busy="off"` opts any region out. Under reduced motion the spinner does not turn and the top bar does not slide.
- `pbBusy(true, region)` sets `aria-busy` on an element or selector, `pbBusy(false, region)` clears it. `pbBusy(true)` with no region shows a thin top progress bar (counted: every `true` needs a `false`).
- htmx recipe: `document.addEventListener('htmx:beforeRequest', e => pbBusy(true, e.detail.target)); document.addEventListener('htmx:afterSettle', e => pbBusy(false, e.detail.target)); document.addEventListener('htmx:sendError', e => pbBusy(false, e.detail.target));` For a page-wide bar use `pbBusy(true)` on `htmx:beforeRequest` and `pbBusy(false)` on `htmx:afterRequest`.
- Announce the result yourself when it matters: `aria-busy` tells assistive tech the region is changing, not what changed.
- A button: `pbBusy(true, button)` swaps its text to `data-busy-label` ("Syncing…"), sets `aria-busy` and `aria-disabled`, and swallows clicks (also Enter and Space) until `pbBusy(false, button)` restores it. Busy labels end in an ellipsis.
