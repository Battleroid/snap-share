The JSON viewer shows structured data as a collapsible tree, in JSON or YAML.

## Markup

- `.pb-json[data-json]` with a toolbar (`[data-json-search]`, a `[data-json-format]` radiogroup `json|yaml`, `[data-json-expand]`, `[data-json-collapse]`, `[data-json-copy]`), an empty `pb-json__body`, `pb-json__path` (`code` and `[data-json-copypath]`) and a `<script type="application/json">` with the data. `data-depth` (default 2) sets how deep it opens.
- `pbJSON(el)` (the `json` hook) renders native `details` nodes with counts when collapsed and syntax colors from the `syn-*` tokens. `el.pbYaml()` returns the YAML text.

## States

- JSON or YAML, from the radiogroup or `data-format`.
- Search highlights matches and opens their parents. A search with no match shows a status line ("Nothing matches “x”.") instead of a silently dimmed tree.
- Copy gives formatted JSON or YAML.

## Keys

- Nodes are native `details`: Enter or Space on a summary toggles it. The path of whatever is under the pointer or focus is shown (`services.jellyfin.ports[0]`).

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: use it for read-only inspection of structured data.
