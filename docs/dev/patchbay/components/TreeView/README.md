Tree views show nested items such as datasets, folders and files.

- Markup: `ul.pb-tree[role=tree][aria-label]` of `li[role=treeitem]` with `.pb-tree__row` (`pb-tree__caret`, icon, `pb-tree__label`, `pb-tree__meta`). Branches carry `aria-expanded` and a child `ul[role=group]`. `aria-selected="true"` marks the selection.
- `pbTree(el)` adds roving focus and keys: ↑/↓ move, → opens or goes to the first child, ← closes or goes to the parent, Home/End, Enter or Space selects. It fires bubbling `pb:expand` and `pb:select` events with the item in `detail`.
- Lazy loading: mark the branch `data-lazy`, listen for `pb:expand`, set `aria-busy` while loading, then fill its group.
- Indent guides and the caret show depth; keep labels to one line with `pb-tree__meta` for size or count.
