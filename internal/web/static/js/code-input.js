// The kit CodeInput (six boxes, bound by the kit's otp hook) over a real
// input[name=code], which is what the form posts. Markup:
//   [data-code-field]
//     [data-code-fallback] > input   the real field; without this script it
//                                    is the only one, and works on its own
//     [data-code-boxes] hidden > .pb-otp[data-otp] > input × 6
// With the script the boxes show and the real input stays in the form,
// visually hidden and out of the tab order. Typing in the boxes writes the
// real input; a value put into the real input (a password manager, a test)
// fills the boxes. The boxes are never read on submit, so a value put into
// the real input alone is posted as it is.

for (const field of document.querySelectorAll("[data-code-field]")) {
  const fallback = field.querySelector("[data-code-fallback]");
  const real = fallback?.querySelector("input");
  const wrap = field.querySelector("[data-code-boxes]");
  const boxes = wrap ? [...wrap.querySelectorAll("input")] : [];
  if (!real || !boxes.length) continue;

  const write = () => { real.value = boxes.map((b) => b.value).join(""); };
  const later = () => setTimeout(write); // the kit's Backspace and paste change boxes without an input event
  wrap.addEventListener("input", write);
  wrap.addEventListener("complete", write);
  wrap.addEventListener("keydown", later);
  wrap.addEventListener("paste", later);
  real.addEventListener("input", () => {
    const digits = real.value.replace(/\D/g, "");
    boxes.forEach((b, i) => { b.value = digits[i] || ""; });
  });

  // The boxes take over: validation points at them, not at the hidden field.
  const focused = document.activeElement === real || real.autofocus;
  real.required = false;
  boxes.forEach((b) => { b.required = true; });
  real.tabIndex = -1;
  real.setAttribute("aria-hidden", "true");
  // The input leaves its box (kept 1px, not 0, so it still takes a value
  // like any field), and the empty box hides.
  fallback.after(real);
  real.classList.add("pb-sr");
  fallback.hidden = true;
  field.querySelector(`label[for="${CSS.escape(real.id)}"]`)?.removeAttribute("for"); // the group names itself by the label's id
  wrap.hidden = false;
  if (focused) boxes[0].focus();
}
