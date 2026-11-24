The query filter turns typed filters like `status:down host:nas-*` into chips.

- Markup: `.pb-qf[data-qf]` with `.pb-qf__box` (icon, input), an empty `ul.pb-qf__menu[hidden]` and a `<script type="application/json">` schema: `{keys: [{key, desc, type: "enum"|"number"|"text", values}]}`. `data-value` sets the starting filters.
- `pbQuery(el)` suggests keys, then values for the key; Nothing is preselected: the arrow keys move through the list, Enter commits the highlighted suggestion (or the text you typed if none is highlighted), and Tab commits only text you typed, closes the list and moves on, so keyboard users are never trapped. Space never commits; it is just a space. Backspace on an empty input edits the last one, and clicking a chip edits it. Syntax: `key:value`, `-key:value` (not), `*` wildcards, `key>n`, `key<n`, quoted free text. Unknown values get a dashed warning chip.
- It fires `pb:change` with the tokens. `el.pbMatch(get)` returns a test function for rows; `el.pbSet(str)` replaces the filters.
- Show the result count next to it and a Clear filters button.
