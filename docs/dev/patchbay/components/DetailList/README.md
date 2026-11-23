Detail lists show label and value pairs for one thing: a host, a share, a job.

- Markup: `dl.pb-dl > div > dt + dd`, with an optional third `dd.pb-dl__act` for a Copy button or an Edit link.
- Labels are mono caps; values are serif text, or `pb-mono` for hosts, ids and numbers. Status values use `pb-status`.
- `pb-dl--stack` puts the label above the value for narrow panes. `pb-dl--grid` lays short pairs out in columns for specs (cpu, memory, disk).
- Keep labels to one or two words. Use a Data table when there are many items with the same fields.
