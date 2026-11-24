Every native input type sits in the same `.pb-input` box as TextInput, so fields line up whatever they hold.

## Markup

- Every native input type sits in the same `.pb-input` box as TextInput, so fields line up whatever they hold. Lay pairs out with `.pb-form__row`.
- Password: leading `pb-icon--key`, trailing `button.pb-input__btn` with `pb-icon--eye` that toggles `type` between `password` and `text` (swap to `eye-off`, update `aria-label`).
- Number: native spinners are hidden; add `pb-input__btn` minus/plus buttons (`data-dec` / `data-inc`) that call `stepDown()` / `stepUp()`. Set `min`, `max`, `step`.
- `email`, `url`, `tel`, `search`: plain, with an affix or icon where it helps.
- `date`, `time`, `datetime-local`, `month`, `week`: native fields, themed by `color-scheme: light` and `accent-color`. Their popups are drawn by the browser and cannot be styled; for a picked date or range use DatePicker, and for a time use TimePicker.
- Color: `input[type="color"]` swatch followed by a read-only hex input kept in sync.
- File: `input[type="file"].pb-file` (outside `.pb-input`); its button matches a secondary `pb-btn`. For drag and drop, use DropZone.
- Password strength: `.pb-strength` (four `<i>` segments and a label `span`) in the same `.pb-field` as the input.

## States

- The `strength` hook sets `data-level` 0–4 and the label (`pbStrings.strength`: weak, fair, good, strong) as you type, from a built-in score (length and mixed character kinds). Plug in your own: `pbStrength.score = (value, input) => level` or `{level, label}`.

## Keys

- Number steppers: the −/+ buttons are `tabindex=-1`, so the field is one tab stop; ↑/↓ step it from the keyboard.
- Native controls otherwise: Tab and Space/Enter as for each element.

## Events

- The steppers dispatch `input` and `change` on the number field.
- `pb:strength` bubbles from the strength meter; change `e.detail.level` / `e.detail.label` in a listener to override the result.

## Do and don't

- Do: use DatePicker, not a native date popup, when you need a styled picker or a range.
- Don't: expect to style the popups of native date and time inputs.
