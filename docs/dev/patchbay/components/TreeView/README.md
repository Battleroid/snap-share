Tree views show nested items such as datasets, folders and files.

## Markup

- `ul.pb-tree[role=tree][aria-label]` of `li[role=treeitem]` with `.pb-tree__row` (`pb-tree__caret`, icon, `pb-tree__label`, `pb-tree__meta`). Branches carry `aria-expanded` and a child `ul[role=group]`.
- Indent guides and the caret show depth.
- Lazy loading: mark the branch `data-lazy`, listen for `pb:expand`, set `aria-busy` while loading, then fill its group. If you fetch the children, the CSP needs `connect-src 'self'` (or your API origin).

## States

- `aria-selected="true"` marks the selection; `aria-expanded` the open branches; `aria-busy` a lazy branch that is loading.

## Keys

- `pbTree(el)` adds roving focus and keys: ↑/↓ move, → opens or goes to the first child, ← closes or goes to the parent, Home/End, Enter or Space selects.

## Events

- Bubbling `pb:expand` (when a branch opens) and `pb:select`, each with the item element in `detail`.

## Do and don't

- Do: keep labels to one line, with `pb-tree__meta` for size or count.
