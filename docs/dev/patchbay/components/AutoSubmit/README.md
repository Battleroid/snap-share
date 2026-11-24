A form that submits when a control changes, for filters and range pickers.

- Markup: `form[data-autosubmit]` (`data-autosubmit="400"` sets the debounce in ms, default 250). Keep a submit button marked `[data-autosubmit-button]`: it stays visible without script and the hook hides it. Controls with `data-autosubmit-skip` and file inputs are ignored.
- It calls `requestSubmit()` after the last change, so validation, `submit` listeners and htmx (`hx-trigger="submit"`) behave as for a click.
- Tell people the control applies at once (a label or help text); changing a setting should not surprise anyone (WCAG 3.2.2).
