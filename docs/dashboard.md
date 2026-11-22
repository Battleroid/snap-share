# Dashboard and albums

`/dashboard` lists everything you have shared: files, pastes, short links and albums. One-time secrets have their own page, `/dashboard/secrets`.

## Finding items

- **Search** matches titles, filenames, the start of text files and pastes, link targets and tags. Every word must match. Words shorter than three characters match titles and filenames only.
- **Type** pills: image, video, audio, text, paste, link, album, other. The counts follow the other filters.
- **Filters**: visibility (unlisted, private, password), expiring within 7 days, album, tag, created from/to (UTC dates), and opened status (not opened, opened in the last 7 days, previewed by a chat app but not opened).
- **Sort**: newest, oldest, name, size, most viewed, last opened, expires soonest.
- **Views**: a table, or a grid of cards with thumbnails. Videos and animated images with a teaser play it when the mouse rests on the card.

The whole view lives in the address, so a filtered view can be bookmarked and the back button works. Pages hold 50 items. The same filters work on `GET /api/v1/items` ([api.md](api.md#list-filters)).

The storage line shows what your files use, against your quota when you have one.

## Working on items

Each row's menu has Copy link, Open, Edit (title, link name, expiry, visibility, password, tags), Add to album and Delete.

Tick rows to act on several at once; the header checkbox selects the page. When the whole page is selected and more items match, **Select all N matching** extends the selection to every match. The server then counts the matches again and refuses if the number changed since the page loaded (something was added, deleted or changed), so an action never reaches items you have not seen. Items uploaded after the page loaded are never included.

Actions on a selection:

| Action | Notes |
|---|---|
| Download zip | files and pastes, uncompressed; links and albums are skipped |
| Set expiry, Set visibility | as editing each item |
| Set password, Remove password | one password for all of them; earlier unlocks stop working |
| Add to album | an existing album or a new one; the items themselves do not change |
| Tags | add or remove tags |
| Send link | emails the links (see below) |
| Delete | asks first; the links stop working and the files are removed |

An action either applies to every selected item or to none. An API key's restrictions (forced visibility, maximum expiry) apply here as they do to single items.

### Send link

Sends each address one email with a fixed text, "riley shared a link with you on snap", and each item's kind and link ("image: https://…"), with no room for a message of your own. Titles, file names and link targets are left out, so the server's address can't carry someone's phishing text. Replies go to your account's email address, which must be verified. Limits: 10 addresses and 20 links per send, and per rolling 24 hours 20 emails per user and 200 for the server (one email per recipient).

Admin settings:

| Setting | Default | |
|---|---|---|
| `email.send_links` | `true` | turns Send link on or off |
| `email.send_links_per_day` | `20` | emails per user per 24 hours |
| `email.send_links_site_per_day` | `200` | emails for the whole server per 24 hours |

Every send is recorded in the audit log (`items.send_link`, with the recipients and item ids). Send link needs SMTP; without it the action is not offered. Secret links are never sent by the server.

## Albums

An album is a shared page with a gallery of your items, in an order you choose. Create one from a selection on the dashboard, or with `POST /api/v1/albums`. Albums have their own link, title, visibility, password and expiry. These apply to the album page only:

- Adding an item to an album never changes the item.
- The album page shows each visitor only the items they could open on their own: unlisted items, and password-protected items that visitor has already unlocked. Private and expired items are hidden. You see every item, with badges for the ones others cannot see.
- An album's password protects the album page, not its items; an expired or private album is gone for others while its items keep working.

Images and videos open in a lightbox on the album page: arrow keys or a swipe move between them, Escape closes it. On your own album page you can move items earlier or later, or take them out; Manage opens the dashboard filtered to the album.

Deleting an album leaves its items alone. Deleting an item takes it out of every album.
