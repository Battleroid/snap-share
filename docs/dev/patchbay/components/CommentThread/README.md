Comment threads hold a conversation on a share, a file or an incident.

- Markup: `ol.pb-thread[data-thread][data-me]` of `li.pb-comment` (avatar, `pb-comment__head` with `b` name and `time.pb-reltime`, `pb-comment__body`, `pb-comment__acts` with `[data-reply]` and `[data-resolve]`, optional `ol.pb-comment__replies`), followed by a `.pb-composer[data-composer]` (avatar, textarea, `[data-post]`).
- `pbThread(th)` posts new comments (⌘↵ or the button; Post stays disabled while empty), opens an inline reply box under a comment, and toggles resolved (dimmed, with a resolved chip). It fires `pb:post` with the text and parent. Bodies render through `pbMarkdown`.
- One level of replies only. Newest at the bottom, next to the composer.
- Words (Reply, Resolve, Reopen, resolved, Post, "just now", the key hint) come from `pbStrings` (`threadReply`, `threadResolve`, `threadReopen`, `threadResolved`, `threadPost`, `threadNow`, `threadHint`).
