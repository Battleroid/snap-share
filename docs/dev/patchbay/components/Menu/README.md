Menu is a popover list of actions, opened from a button with the native `popover` API.

## Markup
- `<button popovertarget="m">` and `<div class="pb-menu" role="menu" id="m" popover>` holding `button.pb-menu__item`s (icon, label, optional `.pb-menu__key`), `hr.pb-menu__sep`, and `.pb-menu__label` headings.
- The `popover` hook places the menu under its trigger when it opens (flipping to stay on screen); `data-align` on the menu or the trigger sets the alignment (for example `end`). You need no script for position.
- `role="menuitemcheckbox"` with `aria-checked="true"` shows a check. `pb-menu__item--danger` goes last, after a separator.
- A destructive item asks first with the confirm hook: add `data-confirm="Title"` (and `data-confirm-body`, `data-confirm-ok`); `.pb-menu__item--danger` makes the dialog a danger one. See Confirm action.

## States
- Checked items (`aria-checked`) show a check; `disabled` items are dimmed and skipped by the keys.
- Danger items are red and sit last, after a separator.

## Keys
- The `menu` hook on `[popover][role=menu]` is built on `pbListKeys`, which apps can use for their own lists. Opening focuses the first item (or `[autofocus]`), ↑/↓ cycle, Home/End jump to the ends, typing letters jumps to the next item that starts with them. Disabled and hidden items are skipped.
- Escape and clicking outside close it natively, and focus goes back to the trigger.

## Events
None of its own. Items are ordinary buttons: listen for `click`. The native `toggle` event fires on the popover when it opens and closes.

## Do and don't
- Do: put the destructive item last, after a separator, and give it `data-confirm`.
- Do: use real `button` elements for items.
- Don't: add your own positioning script; the kit places the menu.
