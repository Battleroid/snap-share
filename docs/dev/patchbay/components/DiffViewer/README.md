The diff viewer shows what changed between two versions of a config file.

- Markup: `.pb-diff[data-diff]` with `pb-diff__head` (icon, `pb-diff__file`, `pb-diff__stat`, a `[data-diff-mode]` radiogroup `unified|split`, `[data-diff-wrap]`), an empty `pb-diff__scroll`, and two `<script type="text/plain">` blocks: `data-diff-old` and `data-diff-new`.
- `pbDiff(el)` computes a line diff, pairs changed lines and marks the changed part of each, folds unchanged runs into "N unchanged lines" buttons, and fills `+added −removed`. `data-context` (default 3) sets how many unchanged lines show around a change; `data-mode` the starting view.
- Removed lines come before added ones. Color is backed by `+`/`−` signs and line numbers in both views.
- Use split view on wide screens for edits, unified for adds and removes and on phones.
