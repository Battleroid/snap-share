The query filter turns typed filters like `status:down host:nas-*` into chips.

## Markup
- `.pb-qf[data-qf]` with `.pb-qf__box` (icon, input), an empty `ul.pb-qf__menu[hidden]` and a `<script type="application/json">` schema: `{keys: [{key, desc, type: "enum"|"number"|"text", values}]}`. `data-value` sets the starting filters. An optional `[data-qf-clear]` button clears them all.
- `pbQuery(el)` (the `qf` hook) suggests keys, then values for the key. Syntax: `key:value`, `-key:value` (not), `*` wildcards, `key>n`, `key<n` (also `>=`, `<=`), quoted free text.
- `el.pbMatch(get)` returns a test function for rows (`get(row, key)`; the key is `null` for free text); `el.pbSet(str)` replaces the filters; `el.pbTokens()` returns them.
- The JSON schema is data in the page, not script, so it works under a strict CSP.

## States
- Nothing is preselected in the suggestion list.
- Unknown values (and a non-number for a number key) get a dashed warning chip; they do not filter anything.
- A chip shows its key, operator and value; a `-` chip is a negation, free text is quoted.

## Keys
- The input is a combobox over a listbox. The arrow keys move through the list, Enter commits the highlighted suggestion (or the text you typed if none is highlighted), and Tab commits only text you typed, closes the list and moves on, so keyboard users are never trapped. Escape closes the list. Space never commits; it is just a space.
- Backspace on an empty input edits the last chip, and clicking a chip edits it. Each chip has Edit and Remove buttons with names.

## Events
- `pb:change` bubbles with the tokens (an array) as `detail` whenever the filters change.

## Do and don't
- Do: show the result count next to it and a Clear filters button.
- Do: use `pbMatch` to filter rows so the syntax means the same everywhere.
- Don't: trap focus in the list; Tab always leaves.
