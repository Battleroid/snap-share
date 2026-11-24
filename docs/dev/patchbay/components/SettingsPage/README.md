The settings page groups an app's options into sections with a nav beside them.

## Markup
- Layout: `.pb-settings` with `ul.pb-settings__nav[data-section-nav]` (links to section ids) and `pb-settings__main` of `section[id]` (`h2`, optional intro `p`, then a `pb-fieldset`). `pbSectionNav(nav)` marks the section in view and scrolls on click; it works inside a scrolling pane.
- Rows: `.pb-setting` with `pb-setting__label`, `pb-setting__desc` and one control (switch, select, button) on the right. Plain fields use `pb-field` and `pb-form__row`.
- Saving: sections with inputs are `form[data-section]` with a `pb-fieldset__foot` (a `pb-foot__status`, Discard, Save). Switches that apply at once need no Save.
- Danger zone last: `pb-fieldset.pb-danger`, each action a `pb-btn--danger` that opens a type-to-confirm Dialog.

## States
- A clean settings section starts with Save and Discard disabled and the status reading "No changes": nothing to save until something changes. The first change enables both (status "unsaved"); returning to the saved values disables them again. Submitting makes it busy.
- After a submit, settle it with `pb:saved` or `pb:error` (`detail.message`) dispatched on the form.
- Under 760px the nav becomes a scrolling row above the sections.
- Inside AppShell the section nav sticks below the shell header (`--pb-head-h`) and scrolls inside itself when long; the highlight follows the section at the top of the visible area.

## Keys
- Clicking a nav link scrolls to the section (smoothly, unless reduced motion is on) and moves focus to its `h2`.
- The current link has `aria-current="true"`.

## Events
- The nav fires none. A section form listens for `pb:saved` and `pb:error`; its Save is an ordinary form submit.

## Do and don't
- Do: put the danger zone last, behind a type-to-confirm Dialog.
- Do: let switches that apply at once skip Save.
- Don't: enable Save on a clean section; it enables when something changes.
