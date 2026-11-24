# Dashboard and albums

The sidebar holds New (the composer, `/`), Items, Albums, Secrets, Stats, Notifications and Settings; admins also get an Admin group. Your account menu is in the header, with Settings and Sign out; the theme (light, dark or system, per browser) is under **Settings › Profile › Appearance**.

**Items** (`/dashboard`) lists everything you have shared: files, pastes, gists, short links and albums. **Albums** is the same page filtered to albums (`/dashboard?type=album`), without the type pills. The heading, count and tab title follow the filter, and the address keeps only the filters you set. One-time secrets have their own page, **Secrets** (`/dashboard/secrets`), and **Stats** (`/dashboard/stats`) adds up the numbers for all your items and secrets ([stats.md](stats.md#your-stats)).

The bulk action bar appears once you select something; type pills with no items are hidden. Rows show up to two tags as pills, each linking to that tag's filter, then a count; hover the count for the full list. Rows and tiles mark private items (eye-off) and items with a password (a yellow password badge); unlisted, the default, has no mark.

Storage is not on the dashboard. When you have a quota, a ring in the header, left of the bell, shows how much you use; it turns yellow at 80% and red at 95%, and opens a popover with used, free and the largest file you can upload. Settings › Profile › Usage has the full view: storage by kind and items by type.

## Finding items

- **Search** matches titles, filenames, the start of text files, pastes and gist files, link targets and tags. Every word must match. Words shorter than three characters match titles and filenames only.
- **Type** pills (on Items): image, video, audio, text, paste, gist, link, album, other. The counts follow the other filters.
- **Filters**: visibility (unlisted, private, password), expiring within 7 days, album, tag, created from/to (UTC dates; a range typed backwards is swapped), and opened status (not opened, opened in the last 7 days, previewed by a chat app but not opened).
- **Sort**: newest, oldest, name, size, most viewed, last opened, expires soonest.
- **Views**: a table, or a grid of cards with thumbnails. Videos and animated images with a teaser play it when the mouse rests on the card.
- **Processing**: a video whose poster is still being made shows "processing" (the kit's media state) in place of its thumbnail, and "no preview" if that failed; the row updates by itself (see [media.md](media.md#processing-state)).

The whole view lives in the address, so a filtered view can be bookmarked and the back button works. Pages hold 50 items. The same filters work on `GET /api/v1/items` ([api.md](api.md#list-filters)).

## Working on items

Each row has a Copy link button; its menu has Open, Details, Edit (title, link name, expiry, visibility, password, tags), Add to album and Delete.

Tick rows to act on several at once (Shift+click, or Shift+Space on a focused box, ticks everything between the last box and this one); the header checkbox, or **Select all on this page** above a grid, selects the page. When the whole page is selected and more items match, **Select all N matching** extends the selection to every match (**Only this page** goes back). The server then counts the matches again and refuses if the number changed since the page loaded (something was added, deleted or changed), so an action never reaches items you have not seen. Items uploaded after the page loaded are never included. After an action that changes items (expiry, visibility, password, tags, an album) the selection stays, so a second action can follow; a delete clears it. The delete confirm names up to three of the items, then "and N more".

Actions on a selection:

| Action | Notes |
|---|---|
| Download zip | files, pastes and gists (a folder each, named after the title or else the slug), uncompressed; links and albums are skipped |
| Set expiry, Set visibility | as editing each item |
| Set password, Remove password | one password for all of them; earlier unlocks stop working |
| Add to album | an existing album or a new one; the items themselves do not change |
| Add to ‹album› | only in add-to mode, see below |
| Tags | add or remove tags |
| Send link | emails the links (see below) |
| Delete | asks first; the links stop working and the files are removed |

## Albums

**Albums** (the Albums type) starts with **New album**: a dialog asks for a title, creates an empty album and opens its page. An empty album's **Add items** opens Items in add-to mode (`/dashboard?add_to=<album id>`): the bulk bar's primary action reads **Add to ‹album title›** and puts the selection in that album (items that are albums, or already in it, are skipped, and the toast says how many). The mode follows the search and filters; **Clear selection** keeps it, and the Items link in the sidebar leaves it. An id that is not one of your own albums is ignored.

On the album page, **Remove from album** asks first, and removing or reordering says so with a toast, or with a notice above the tiles if the server refused.

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
- The album page shows each visitor only the items they could open on their own: unlisted items, and password-protected items that visitor has already unlocked. Private and expired items are hidden. You see every item, marked when others cannot see it (private, password or expired).
- An album's password protects the album page, not its items; an expired or private album is gone for others while its items keep working.

Images, videos and audio open in a viewer on the album page: arrow keys or a swipe move between them, Escape closes it. On your own album page you can move items earlier or later, or take them out; Manage opens the dashboard filtered to the album.

Deleting an album leaves its items alone. Deleting an item takes it out of every album.
