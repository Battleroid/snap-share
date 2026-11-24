Disclosure uses native `<details>`/`<summary>` for optional detail and FAQ-style accordions.

## Markup

- Consumer provides: `details.pb-details` > `summary` (text, optional `.pb-details__meta`) + `.pb-details__body`.
- Accordion: wrap several in `.pb-accordion`; give them the same `name` to allow only one open at a time.

## States

- Open or closed through the native `open` attribute.

## Keys

- Native: Tab to the summary, Enter or Space toggles it.

## Events

- The native `toggle` event on the `details`; the component fires no `pb:` events.

## Do and don't

- Do: put advanced or rarely needed settings here.
- Don't: hide required fields in a disclosure.
