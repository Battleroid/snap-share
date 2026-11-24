// Field errors for forms a page sends itself (fetch): the same markup the kit's
// pbValidate writes (aria-invalid on the control, data-invalid on its
// .pb-field, a .pb-field__error joined to aria-describedby), so an error the
// server answers with sits on the field it is about and the kit clears it as
// the person types.

let seq = 0;

function describe(control, id, remove) {
  const ids = (control.getAttribute("aria-describedby") || "").split(/\s+/).filter((x) => x && x !== id && x !== remove);
  if (id) ids.push(id);
  if (ids.length) control.setAttribute("aria-describedby", ids.join(" "));
  else control.removeAttribute("aria-describedby");
}

// setFieldError marks control invalid with message under it.
export function setFieldError(control, message) {
  if (!control.id) control.id = `field-error-${++seq}`;
  control.setAttribute("aria-invalid", "true");
  const field = control.closest(".pb-field");
  if (!field) return;
  field.setAttribute("data-invalid", "");
  let err = field.querySelector(".pb-field__error");
  if (!err) {
    err = document.createElement("span");
    err.className = "pb-field__error";
    err.id = `${control.id}-error`;
    field.append(err);
  }
  const icon = document.createElement("span");
  icon.className = "pb-icon pb-icon--status-down pb-icon--16";
  icon.setAttribute("aria-hidden", "true");
  err.replaceChildren(icon, message);
  describe(control, err.id);
}

// clearFieldError removes the error setFieldError or the kit put on control.
export function clearFieldError(control) {
  control.removeAttribute("aria-invalid");
  const field = control.closest(".pb-field");
  if (!field) return;
  const err = field.querySelector(".pb-field__error");
  if (err) describe(control, null, err.id);
  if (!field.querySelector('[aria-invalid="true"]')) {
    err?.remove();
    field.removeAttribute("data-invalid");
  }
}

// clearFieldErrors clears every marked control under root.
export function clearFieldErrors(root) {
  for (const c of root.querySelectorAll('[aria-invalid="true"]')) clearFieldError(c);
}

// reid gives every id under root a suffix, and the references to it (for,
// aria-controls, aria-describedby, aria-labelledby) the same one, so a copy of
// a block of fields has ids of its own.
export function reid(root, suffix) {
  const ids = new Map();
  for (const e of root.querySelectorAll("[id]")) {
    ids.set(e.id, `${e.id}-${suffix}`);
    e.id = ids.get(e.id);
  }
  const refs = { for: false, "aria-controls": true, "aria-describedby": true, "aria-labelledby": true, "aria-owns": true };
  for (const e of root.querySelectorAll("*")) {
    for (const [attr, list] of Object.entries(refs)) {
      const v = e.getAttribute(attr);
      if (!v) continue;
      const next = (list ? v.split(/\s+/) : [v]).map((x) => ids.get(x) || x).join(" ");
      if (next !== v) e.setAttribute(attr, next);
    }
  }
}
