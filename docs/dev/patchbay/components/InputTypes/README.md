Every native input type sits in the same `.pb-input` box as TextInput, so fields line up whatever they hold.

- Password: leading `pb-icon--key`, trailing `button.pb-input__btn` with `pb-icon--eye` that toggles `type` between `password` and `text` (swap to `eye-off`, update `aria-label`).
- Number: native spinners are hidden; add `pb-input__btn` minus/plus buttons that call `stepDown()` / `stepUp()`. Set `min`, `max`, `step`.
- `email`, `url`, `tel`, `search`: plain, with an affix or icon where it helps.
- `date`, `time`, `datetime-local`, `month`, `week`: native fields, themed by `color-scheme: light` and `accent-color`. Their popups are drawn by the browser and cannot be styled; for a picked date or range use DatePicker.
- Color: `input[type="color"]` swatch followed by a read-only hex input kept in sync.
- File: `input[type="file"].pb-file` (outside `.pb-input`); its button matches a secondary `pb-btn`. For drag and drop, use DropZone.
- Lay pairs out with `.pb-form__row`.
- Password strength: `.pb-strength` (four `<i>` segments and a label `span`) under the field, with `data-level` 0–4 from your own scoring (weak, fair, good, strong).
- Time: for picking, use TimePicker.
