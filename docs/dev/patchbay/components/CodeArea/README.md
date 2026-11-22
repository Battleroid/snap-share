CodeArea is an editable, syntax-highlighted text area with line numbers, for configs and scripts.

- Consumer provides: `.pb-code` > `.pb-code__bar` (file name, `.pb-code__lang`, actions) and `.pb-code__body` > `.pb-code__gutter` + `.pb-code__edit` > `.pb-code__hl` (highlighted, `aria-hidden`) + `textarea.pb-code__input`.
- The textarea sits on top with transparent text; on every `input`, re-render `.pb-code__hl` and the gutter from its value. The preview carries a 20-line YAML highlighter as a reference.
- Token classes: `tok-k` (`syn-keyword`), `tok-s` (`syn-string`), `tok-n` (`syn-number`), `tok-f` (`syn-function`), `tok-t` (`syn-type`), `tok-c` (`syn-comment`, italic), `tok-p` (punctuation, `ink-muted`). Map any highlighter (Prism, Shiki, highlight.js) onto these.
- Read-only blocks drop the textarea and keep `.pb-code__hl`.
