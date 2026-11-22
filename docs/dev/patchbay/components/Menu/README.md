Menu is a popover list of actions, opened from a button with the native `popover` API.

- Consumer provides: `<button popovertarget="m">` and `<div class="pb-menu" role="menu" id="m" popover>` holding `button.pb-menu__item`s (icon, label, optional `.pb-menu__key`), `hr.pb-menu__sep`, and `.pb-menu__label` headings.
- Position it under its trigger on the `toggle` event (or with CSS anchor positioning where supported); `[popover]` closes on Escape and outside click.
- `role="menuitemcheckbox"` with `aria-checked="true"` shows a check. `pb-menu__item--danger` goes last, after a separator.
