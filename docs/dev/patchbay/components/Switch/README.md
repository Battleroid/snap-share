Switches are `<input type="checkbox" role="switch" class="pb-switch">` for settings that take effect immediately.

## Markup

- A `label.pb-choice` with the text first and the switch last (`justify-content: space-between` for a settings row).

## States

- On is `accent` with an `on-accent` thumb; off is `surface-sunk` with an `ink-muted` thumb. Hard variant squares both.

## Keys

Native controls only: Tab to focus and Space to toggle, as for the checkbox. `role="switch"` announces on/off.

## Events

Native `input` and `change`; no `pb:` events.

## Do and don't

- Do: name the setting ("Maintenance mode"), not the state or the action.
- Don't: use it for a choice that needs a Save; use a checkbox.
