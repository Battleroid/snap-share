The settings page groups an app's options into sections with a nav beside them.

- Layout: `.pb-settings` with `ul.pb-settings__nav[data-section-nav]` (links to section ids) and `pb-settings__main` of `section[id]` (`h2`, optional intro `p`, then a `pb-fieldset`). `pbSectionNav(nav)` marks the section in view and scrolls on click; it works inside a scrolling pane.
- Rows: `.pb-setting` with `pb-setting__label`, `pb-setting__desc` and one control (switch, select, button) on the right. Plain fields use `pb-field` and `pb-form__row`.
- Saving: sections with inputs are `form[data-section]` with a `pb-fieldset__foot` (status, Discard, Save). Switches that apply at once need no Save.
- Danger zone last: `pb-fieldset.pb-danger`, each action a `pb-btn--danger` that opens a type-to-confirm Dialog.
- Under 760px the nav becomes a scrolling row above the sections.
- Inside AppShell the section nav sticks below the shell header (`--pb-head-h`) and scrolls inside itself when long; the highlight follows the section at the top of the visible area.
