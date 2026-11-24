// Server-rendered field errors (a wrong sign-in, a taken username, a weak
// password): the form comes back with aria-invalid on the controls at fault
// and a .pb-field__error under each. Focus goes to the one marked
// data-focus-error, else the first invalid control, so the person can type
// the correction at once; the error is read as the field is focused.
const bad = document.querySelectorAll('[aria-invalid="true"]');
const target = document.querySelector('[aria-invalid="true"][data-focus-error]') || bad[0];
if (target && !target.closest("[hidden]")) target.focus({ preventScroll: false });

// A form marked data-autofocus-first has no autofocus-capable field of its
// own (the pb.password partial): focus its first text or password input
// unless an error already took focus.
if (!target) document.querySelector("[data-autofocus-first] input:is([type=password],[type=text]):not([hidden])")?.focus();
