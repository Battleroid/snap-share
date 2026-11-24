CodeArea is an editable, syntax-highlighted text area with line numbers, for configs and scripts.

## Markup

- `.pb-code` > `.pb-code__bar` (file name, `.pb-code__lang`, actions) and `.pb-code__body` > `.pb-code__gutter` + `.pb-code__edit` > `.pb-code__hl` (highlighted, `aria-hidden`) + `textarea.pb-code__input`.
- The textarea sits on top with transparent text. The `codearea` hook keeps `.pb-code__hl` and the gutter in step with it: on every `input`, on form reset, when script sets `textarea.value`, and on `el.pbSync()`. A textarea added or filled later needs `pbInit` and no other glue.
- Token classes: `tok-k` (`syn-keyword`), `tok-s` (`syn-string`), `tok-n` (`syn-number`), `tok-f` (`syn-function`), `tok-t` (`syn-type`), `tok-c` (`syn-comment`, italic), `tok-p` (punctuation, `ink-muted`). Map any highlighter (Prism, Shiki, highlight.js) onto these.
- Read-only blocks drop the textarea and keep `.pb-code__hl`; their text stays selectable. The hook adds a Copy button (`.pb-code__copy`, in the bar when there is one) that copies the code with the CopyField behavior; `data-nocopy` on `.pb-code`, or your own `[data-copy-target]` button inside it, skips that.
- Highlighting: the kit ships one small reference highlighter, `pbCodeHL(text)`, for YAML-like config (keys, strings, numbers, comments); the hook runs it when `.pb-code` has `data-lang="yaml"` or `data-highlight`, and otherwise mirrors the text as typed (escaped). An editable area without `data-lang="yaml"` is not YAML-highlighted. Plug in any highlighter with `pbCodeArea.highlight = (text, lang, el) => html` (return escaped markup with `tok-*` spans, or `null` to fall back); `lang` is `data-lang`. A read-only block that already holds `tok-*` markup is left as it is. `patchbay.js` has no general-purpose highlighter.

## States

Editable (textarea) or read-only (no textarea). Copy shows the CopyField "Copied" state for 1.6 s.

## Keys

The textarea is the focus target and behaves natively; the highlight layer is `aria-hidden`. Give the textarea an accessible name.

## Events

Native `input` and `change` events from the textarea; the component adds none.

## Do and don't

- Do: map your highlighter onto the `tok-*` classes and return escaped markup.
- Don't: expect a general-purpose highlighter in `patchbay.js`.
