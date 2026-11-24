Ask before an action, then do it for real.

- Markup: add `data-confirm="Title"` to a `button`, a submit button inside a form, an `a`, or a `form`. Optional: `data-confirm-body`, `data-confirm-ok` (the action's label, not "OK"), `data-confirm-cancel`, `data-confirm-variant="danger"` (the default on `.pb-btn--danger` and `.pb-menu__item--danger`), `data-confirm-type="word"` (type-to-confirm: the button stays disabled until the word is typed).
- The hook opens `pbConfirm`; on confirm it clicks the control again (or resubmits the form), so the button's `name` and `value`, `formaction`, htmx attributes and a link's `href` all apply. Cancel returns focus to the control.
- Without script the control just does its action, with no disabled-until-wired trap. For irreversible actions, also check on the server, or point the control's `href` or `formaction` at a confirm page.
- Body text is escaped. Say what is lost and whether it can be undone.
