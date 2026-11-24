The JSON viewer shows structured data as a collapsible tree, in JSON or YAML.

- Markup: `.pb-json[data-json]` with a toolbar (`[data-json-search]`, a `[data-json-format]` radiogroup `json|yaml`, `[data-json-expand]`, `[data-json-collapse]`, `[data-json-copy]`), an empty `pb-json__body`, `pb-json__path` (`code` and `[data-json-copypath]`) and a `<script type="application/json">` with the data. `data-depth` (default 2) sets how deep it opens.
- `pbJSON(el)` renders native `details` nodes with counts when collapsed, syntax colors from the `syn-*` tokens, search that highlights matches and opens their parents, and the path of whatever is under the pointer or focus (`services.jellyfin.ports[0]`). Copy gives formatted JSON or YAML.
- `el.pbYaml()` returns the YAML text.
- A search with no match shows a status line ("Nothing matches “x”.") instead of a silently dimmed tree.
