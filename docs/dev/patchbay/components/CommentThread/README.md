Comment threads hold a conversation on a share, a file or an incident.

## Markup

- `ol.pb-thread[data-thread][data-me]` of `li.pb-comment` (avatar, `pb-comment__head` with `b` name and `time.pb-reltime`, `pb-comment__body`, `pb-comment__acts` with `[data-reply]` and `[data-resolve]`, optional `ol.pb-comment__replies`), followed by a `.pb-composer[data-composer]` (avatar, textarea, `[data-post]`).
- Bodies render through `pbMarkdown`.
- Words (Reply, Resolve, Reopen, resolved, Post, "just now", the key hint) come from `pbStrings` (`threadReply`, `threadResolve`, `threadReopen`, `threadResolved`, `threadPost`, `threadNow`, `threadHint`).

## States

- Post stays disabled while the textarea is empty.
- Resolved comments are dimmed and carry a resolved chip (`data-resolved`).
- One level of replies only. Newest at the bottom, next to the composer.

## Keys

⌘↵ (Ctrl+Enter) posts; Esc cancels an inline reply box. Reply opens an inline box under the comment and focuses it.

## Events

`pb:post` bubbles from the thread when a comment or reply is posted, `detail` `{text, parent}` (`parent` is the comment element for a reply, `null` for a new top-level comment). The comment is added to the DOM first; saving it is the app's job.

## Do and don't

- Do: save the comment from the `pb:post` handler.
- Don't: nest replies deeper than one level.
