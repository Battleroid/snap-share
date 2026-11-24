Detail lists show label and value pairs for one thing: a host, a share, a job.

## Markup

- `dl.pb-dl > div > dt + dd`, with an optional third `dd.pb-dl__act` for a Copy button or an Edit link.
- Labels are mono caps; values are serif text, or `pb-mono` for hosts, ids and numbers. Status values use `pb-status`.

## States

- `pb-dl--stack` puts the label above the value for narrow panes. `pb-dl--grid` lays short pairs out in columns for specs (cpu, memory, disk).

## Keys

- Native `dl` semantics; the term and its value are read as a pair. Buttons and links in `dd.pb-dl__act` are ordinary tab stops.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: keep labels to one or two words.
- Don't: use it for many items with the same fields; use a Data table.
