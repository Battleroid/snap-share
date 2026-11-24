Loading indicators show that something is on its way.

## Markup

- Spinner: `span.pb-spinner` (`--sm`, `--lg`) with `role="status"` and an `aria-label`.
- Inline: `.pb-loading` pairs a small spinner with italic text that says what is happening ("Checking 12 services…").
- Skeletons: `pb-skel` blocks (`--text`, `--title`, `--circle`, `--block`) shaped like the content they replace, so nothing jumps when the real content swaps in. The last example runs live: skeleton and spinner, then the real card swaps in.

## States

- In the hard cut the spinner is a turning square.
- Under reduced motion the shimmer becomes a slow fade and spinners turn slower; they never stop, so a wait never looks frozen.
- Buttons use their own busy state.

## Keys

- Put `aria-busy="true"` and an `aria-label` on the skeleton's container. A skeleton that stands in for a card is `role="group"` with `aria-busy="true"` and an `aria-label` naming what is loading (a plain `div` may not carry `aria-label`).
- The spinner's `role="status"` announces its label.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: show nothing for waits under 300 ms, skeletons for first loads, a spinner for actions.
- Do: shape skeletons like the final layout.
