A form that submits when a control changes, for filters and range pickers.

## Markup

- `form[data-autosubmit]` (`data-autosubmit="400"` sets the debounce in ms, default 250).
- Keep a submit button marked `[data-autosubmit-button]`: it stays visible without script and the hook hides it.
- Controls with `data-autosubmit-skip` and file inputs are ignored.
- Without script the form still works with its visible button.

## States

The submit button is hidden by the hook; nothing else changes visibly.

## Keys

Native controls only: Tab and Space/Enter as for each element. Each `change` restarts the debounce.

## Events

- It calls `requestSubmit()` after the last `change`, so validation, `submit` listeners and htmx (`hx-trigger="submit"`) behave as for a click. The component fires no events of its own.

## Do and don't

- Do: tell people the control applies at once (a label or help text); a setting that changes on its own should not surprise anyone (WCAG 3.2.2).
- Do: use it for filters and range pickers.
- Don't: use it for forms with destructive or costly submits.
