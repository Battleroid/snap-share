A region whose content is being replaced.

## Markup

- `[aria-busy="true"]` dims its children, blocks the pointer and shows a spinner, all after 300 ms so a fast swap never flashes. The inbox panel and tree items keep their own loading look; `data-busy="off"` opts any region out.
- A button takes `data-busy-label` ("Syncing…") for its busy text. Busy labels end in an ellipsis.

## States

- Region: `pbBusy(true, region)` sets `aria-busy` on an element or selector, `pbBusy(false, region)` clears it.
- Page: `pbBusy(true)` with no region shows a thin top progress bar (counted: every `true` needs a `false`).
- Button: `pbBusy(true, button)` swaps its text to `data-busy-label`, sets `aria-busy` and `aria-disabled`, and swallows clicks (also Enter and Space) until `pbBusy(false, button)` restores it.
- Under reduced motion the spinner does not turn and the top bar does not slide.

## Keys

`aria-busy` tells assistive tech the region is changing, not what changed. The top bar has `role=progressbar`. Announce the result yourself when it matters.

## Events

None; the component fires no events. It is driven by calls, for example from htmx events:
`document.addEventListener('htmx:beforeRequest', e => pbBusy(true, e.detail.target)); document.addEventListener('htmx:afterSettle', e => pbBusy(false, e.detail.target)); document.addEventListener('htmx:sendError', e => pbBusy(false, e.detail.target));`
For a page-wide bar call `pbBusy(true)` on `htmx:beforeRequest` and `pbBusy(false)` on `htmx:afterRequest`.

## Do and don't

- Do: clear busy on every outcome, including errors.
- Do: announce the result when it matters.
- Don't: leave a page-wide `pbBusy(true)` without its matching `false`.
