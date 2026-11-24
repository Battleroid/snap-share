Ask before an action, then do it for real.

## Markup

- Add `data-confirm="Title"` to a `button`, a submit button inside a form, an `a`, or a `form`. Optional: `data-confirm-body`, `data-confirm-ok` (the action's label, not "OK"), `data-confirm-cancel`, `data-confirm-variant="danger"` (the default on `.pb-btn--danger` and `.pb-menu__item--danger`), `data-confirm-type="word"` (type-to-confirm: the button stays disabled until the word is typed).
- The hook opens `pbConfirm`; on confirm it clicks the control again (or resubmits the form), so the button's `name` and `value`, `formaction`, htmx attributes and a link's `href` all apply.
- Body text is escaped.

## States

- Without script the control just does its action, with no disabled-until-wired trap.
- To send nothing unconfirmed without script, render the control `disabled` with `data-confirm-enable` (on a form: on its submit buttons). The hook enables it once it is bound; without script it stays disabled.
- While the dialog is open, a second click on the control is ignored.

## Keys

Cancel returns focus to the control. The dialog is a modal `pbConfirm` (focus trapped, Esc cancels).

## Events

None; the component fires no events of its own.

## Do and don't

- Do: say what is lost and whether it can be undone.
- Do: for irreversible actions, also check on the server, or point the control's `href` or `formaction` at a confirm page.
- Don't: use "OK" as the confirm label.
