// The kit CodeInput (six boxes, bound by the kit's codeinput hook) in place
// of a plain input[name=code]. Markup (templates/_partials/pb.html, pb.codeinput):
//   [data-code-field]
//     [data-code-fallback] > input   the plain field; without this script it
//                                    is the only one, and posts the code
//     [data-code-boxes] hidden > .pb-otp[data-codeinput][data-name=code]
// With the script the plain input goes and the boxes show. The kit's
// data-name adds a hidden input[name=code] that always holds the whole code,
// so the form posts it as it is.

for (const field of document.querySelectorAll("[data-code-field]")) {
  const fallback = field.querySelector("[data-code-fallback]");
  const real = fallback?.querySelector("input");
  const wrap = field.querySelector("[data-code-boxes]");
  const boxes = wrap ? [...wrap.querySelectorAll("input")] : [];
  if (!real || !boxes.length) continue;

  const focused = document.activeElement === real || real.autofocus;
  boxes.forEach((b) => { b.required = true; });
  field.querySelector(`label[for="${CSS.escape(real.id)}"]`)?.removeAttribute("for"); // the group names itself by the label's id
  fallback.remove();
  wrap.hidden = false;
  if (focused) boxes[0].focus();
}
