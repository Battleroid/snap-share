# Choosing components

This guide is for AI agents designing or building screens with Patchbay. It tells you which component to use for each job, so you pick from the kit instead of guessing or building your own. Each entry is drawn from that component's README; when the two disagree, the README wins, and the guide should be fixed.

## How to use it

1. **Find the job, not the widget.** Look up what the person needs to do in the decision sections below. Each answer names one component and the one it is most often confused with.
2. **Read the component's README** (`components/<Name>/README.md`) for markup, states, keys and events, then **copy the markup from `components/<Name>/example.html`**. Don't write `pb-` markup from memory, and never invent a `pb-` class.
3. **If nothing here fits, say so.** Compose existing components first. Only design something new if no combination works, and flag it as new rather than presenting it as part of the kit.

Rules that hold everywhere (see DESIGN.md for the full guide):

- State is always icon + word + color, never color alone.
- One primary button per view. Danger buttons are for destructive actions only, never Cancel.
- No inline `style` attributes or inline scripts: use `data-vars`, `data-style` with `pbStyled()`, or the attribute forms (`data-span`, `data-n`, `data-value`).
- Tokens only (`var(--accent)`, `var(--space-6)`), never raw hex or px.
- Sentence case for titles and buttons, lowercase for badge and pill words, mono for hosts, paths, sizes and times. No emoji.
- One edge (`soft` or `hard`) and one density per view.

## 1. Start with the frame

| The screen is | Start from | Not |
| --- | --- | --- |
| Any app with navigation | AppShell (skip link, header, side nav, page header, footer) | SiteHeader |
| A public website or docs site | SiteHeader | AppShell |
| A phone-first app with 4–5 top-level places | MobileNav tab bar | AppShell's drawer |
| An overview of a system's health | Dashboard (12-column `pb-dash`) | a stack of Cards |
| Instance or app-wide settings | SettingsPage | ProfilePage |
| One person's own account | ProfilePage | SettingsPage |
| Sign-in, two-factor setup, a code prompt | SignIn | a Dialog |
| API tokens | ApiTokens | a DataTable you build |
| A public status page | StatusPage | Dashboard |
| A document meant to be printed | PrintReport (`pb-report`) | Prose alone |
| An email the app sends | AlertEmail (`email/alert.html`) | anything else in the kit; email needs its own inline styles |

Inside AppShell a page is `pb-pagehead` (Breadcrumbs, title, description) then `pb-layout` (content plus a 300px aside). Every app page gets a SkipLink, the AppIcons head snippet, and a ThemeSwitch in the header tools.

## 2. Getting around

| Need | Use | Not |
| --- | --- | --- |
| Show where a page sits and link back up | Breadcrumbs (skip on top-level pages) | Tabs |
| Jump to any page, host or action by typing | CommandPalette (⌘K) | a search field in the header |
| Switch between views of one object | Tabs (2–6, one or two words) | Segmented |
| Filter a list in place | Segmented or Pill filters | Tabs |
| Move through long results | Pagination ("101–120 of 312") | Load more, unless it's a feed |
| Nested storage, datasets, folders | TreeView | nested ListItems |
| List the keyboard shortcuts | Shortcuts sheet on `?` (mirror the palette) | a help page |
| Jump past the header by keyboard | SkipLink | nothing; every app page needs one |
| A section nav beside settings | SettingsPage's `data-section-nav` | Tabs |

## 3. Actions

| Need | Use | Not |
| --- | --- | --- |
| Trigger one action | Button (`pb-btn`, verb label) | Link |
| Go somewhere | Link (`pb-link`, says where it goes) | Button |
| Several actions on the same object (Start, Stop, Restart) | ButtonGroup | Segmented |
| A row of mixed controls above a table | `pb-toolbar` (ButtonGroup) | free-floating buttons |
| More actions than fit | Menu (destructive item last, after a separator) | a row of ghost buttons |
| Actions on the thing under the pointer | ContextMenu, always duplicated elsewhere | ContextMenu as the only route |
| Act on many selected rows | BulkActions bar | per-row buttons repeated |
| Ask before a destructive or costly action | ConfirmAction (`data-confirm`) or `pbConfirm` | a toast warning |
| Something irreversible with data attached | Dialog type-to-confirm (`pbConfirm({typeToConfirm})`) | plain confirm |
| Share a URL with copy and QR | ShareLink | CopyField (that's for one-time secrets) |
| Set who can open a link and on what terms | ShareDialog | a custom form |

## 4. Choosing a value

Pick by the kind of choice, then by count:

| The choice is | Use | Not |
| --- | --- | --- |
| On/off that applies at once | Switch | Checkbox |
| On/off saved with a form | Checkbox | Switch |
| Several independent options in a form | Checkbox list (`.pb-choices`) | TagInput |
| 2–6 exclusive options, each needing a hint | Radio | Select |
| 2–5 short exclusive options in a toolbar (views, time windows) | Segmented | Radio, ButtonGroup |
| Options that need a description, figure or icon | ChoiceCard | Radio |
| More than six fixed options | Select | Radio |
| One value from a long or searchable list (hosts, users, images) | Combobox | Select (fine under ~10) |
| Many short values (CIDRs, emails, labels) | TagInput | Combobox |
| A rough position (fan speed, retention) | Range | a number input |
| An exact number | a number input (InputTypes) | Range |
| A minimum and maximum | DualRange (log scale for sizes) | two number inputs |
| A date or date range | DatePicker (or native `type="date"` if its popup is acceptable) | a time field |
| A time of day | TimePicker | DatePicker |
| A time window for logs and metrics | TimeRange (with auto-refresh) | two DatePickers |
| A recurring schedule | CronBuilder | a free-text cron field alone |
| A color | ColorPicker (kit swatches plus hex) | native `type="color"` |
| A filter like `status:down host:nas-*` | QueryFilter | a plain search box |
| Light, dark or system | ThemeSwitch | Segmented you wire yourself |

## 5. Entering text and data

| Need | Use | Not |
| --- | --- | --- |
| A single line of text | TextInput (field pattern: label, control, help) | a bare input |
| Email, number, URL, password, search and other native types | InputTypes, same `.pb-input` box | custom styling |
| A verification or 2FA code (digits) | CodeInput | TextInput |
| A value people copy but don't edit (token, path, link) | CopyField (reveal for secrets) | a readonly TextInput |
| Environment variables, headers, labels | KeyValue, inside a Fieldset | a textarea |
| Formatted notes, descriptions, comments | MarkdownEditor (render with `pbMarkdown`) | a rich-text editor |
| A config file or script | CodeArea | a textarea |
| A profile photo | AvatarEditor (saves on its own) | DropZone |
| A group of related fields with Save and Discard | Fieldset (actions inside the panel) | buttons floating below |
| A form with more than two fields | add an ErrorSummary at the top | inline errors only |
| A filter form that applies as you change it | AutoSubmit (say it applies at once) | AutoSubmit for destructive submits |
| Which events go to which channel | NotifySettings | a grid of Switches you build |

## 6. Files and media

| Need | Use | Not |
| --- | --- | --- |
| Accept files by drag or browse | DropZone (upload settings in its DropSet) | a bare file input |
| Show each upload's progress, retry, cancel | UploadQueue | a Progress bar per file you build |
| Browse a folder | FileBrowser (list view except photo folders) | ListItem |
| Preview the selected file beside the browser | FilePreview, in a Resizable split | a Dialog |
| A gallery that opens full screen | ImageViewer | Media cards |
| Play video or audio | MediaPlayer, never native `controls` | `<video controls>` |
| One shared item (media, title, meta, actions) | Media | a Card you assemble |
| Many videos with thumbnails | MediaList | FileBrowser |
| Media still processing, or a hover teaser | MediaState | a bare skeleton |
| A list and a preview pane, or editor and output | Resizable | a fixed two-column grid |

## 7. Showing state and feedback

Pick by who caused it and when:

| What happened | Use | Not |
| --- | --- | --- |
| The result of the person's own action (with Undo) | Toast | Notice |
| A problem on this page or in this form | Notice | Toast |
| Something that happened while they were away | Notifications (bell and inbox) | Toast, Timeline |
| App-wide: maintenance, offline, a full pool, an update | SiteBanner (one at a time) | Notice |
| The whole system's state on a status page | StatusPage's banner | SiteBanner |
| A card's own warning | Card band | Notice inside the card |
| A decision that can't wait | Dialog | Popover |
| They're about to be signed out | SessionTimeout | Toast |
| An item's state (up, degraded, down) | Badge (icon, color and word) | Pill |
| A tag people created, or a filter chip | Pill | Badge |
| A person's presence | Avatar with a `pb-dot` | a colored border |
| Work with a known end | Progress (`progress`) | Loading |
| A measurement in a range (disk, quota) | Progress (`meter`) | Gauge, for a small inline bar |
| Something is on its way | Loading: nothing under 300 ms, a skeleton for first loads, a spinner for actions | a spinner on first load |
| A region whose content is being replaced | BusyRegion (`aria-busy`) | Loading over everything |
| Storage use in the app header | StorageRing (with a breakdown popover) | a Gauge in the header |
| An empty list, no search matches, a failed page | EmptyState (title is what happened, body is what to do) | a blank area |

## 8. Small overlays

| Need | Use | Not |
| --- | --- | --- |
| A short label on hover and focus (under six words) | Tooltip | Popover |
| A few facts and at most two actions next to a control | Popover (or a toggletip when the text matters) | Tooltip |
| Anything longer, or that needs a decision | Dialog | Popover |
| A form or details that can wait in a side panel | Dialog drawer (`pb-dialog--drawer`) | a Popover |
| An item's actions on a phone | MobileNav action sheet | Menu |

## 9. Showing data

**One thing or many?**

| Need | Use | Not |
| --- | --- | --- |
| The fields of one thing (a host, a share, a job) | DetailList | DataTable |
| A set of things on one line each, opened or acted on | ListItem | DataTable |
| Many things people compare or sort by column | DataTable | ListItem |
| A big table needing columns, resize, density, inline edit, CSV | TableTools | DataTable alone |
| Past about 2,000 rows | VirtualList | DataTable (below that, render normally so browser find works) |
| A record of events, grouped by day | Timeline | Notifications |
| Structured data to inspect read-only | JsonViewer | a `<pre>` |
| A conversation on a share, file or incident | CommentThread | Timeline |
| Reorderable things (boot order, tiles) | Sortable (keyboard path included) | drag without keys |
| Scheduled work by day | EventCalendar | Timeline |
| A pool's space split across datasets | StorageBar (four segments at most) | a donut |
| Hardware by rack unit | Rack | Diagram |
| Topology: nodes, zones, edges | Diagram | Flow |
| Which way data or requests move | Flow | Diagram |
| Config changes between two versions | DiffViewer | CodeArea |
| What a command printed, with its exit status | Terminal | LogViewer |
| One service's live log | LogViewer | Terminal |
| Logs from many labeled streams (Loki-style) | LogExplorer | LogViewer |
| A QR for a phone to open a link or join a network | QRCode (always with the link beside it) | an image |

**Charts.** Pick by the question:

| The question is | Use | Not |
| --- | --- | --- |
| How has it changed over time? | Chart (with TimeRange for logs and metrics) | BarChart |
| How do a few categories compare? | BarChart (stacked or horizontal, four series at most) | Chart |
| How are values distributed? | Histogram | BarChart |
| Where is one value against its limits? | Gauge (dial) | a donut |
| What are the parts of a whole? | Gauge's donut (five parts at most) | more than five slices; use horizontal bars |
| What happened each day across the year? | Heatmap | Chart |
| What's the trend beside this number? | Sparkline (print the number too) | a full Chart |
| A headline metric with its history | StatTile (four headline tiles at most on a dashboard, one hero per page) | a Card with a Chart |
| A chart that needs a ground, ranges or limits | ChartGround (`pb-chart--ink` once per page) | Chart alone |

## 10. Layout pieces and text

| Need | Use | Not |
| --- | --- | --- |
| A base surface | Card (`pb-card--outline` if it's picked or opened) | nested Cards |
| Optional detail, advanced settings, FAQs | Disclosure (`<details>`) | hidden required fields |
| Progress through a fixed sequence (setup, restore) | Steps (under six) | Tabs |
| Long-form reading text | Prose (`pb-prose`) | Prose for app chrome |
| Times, long names, hashes, paths in tight space | TextUtils (relative time, middle truncation, clamp) | truncating something people must type |
| A person | Avatar (`data-name` always) | an icon |
| Tight rows for logs and admin lists | Density `compact` | compact on phones or forms |
| Icons | Icons (`pb-icon pb-icon--NAME`, with a word for every status icon) | an icon as the only signal |
| Scrolling regions | Scrollbars (automatic; name the region) | hiding the scrollbar |

## Commonly confused

- **Switch or Checkbox:** Switch applies at once; Checkbox waits for Save.
- **Segmented, Radio, Select or ButtonGroup:** Segmented is a short exclusive choice in a toolbar, Radio is 2–6 options with hints, Select is more than six, and ButtonGroup is actions, not choices.
- **Tabs or Segmented:** Tabs switch views of one object; Segmented filters a list in place.
- **Toast, Notice, Notifications, SiteBanner or Dialog:** your own action, a problem here, what happened while you were away, app-wide state, a decision that can't wait.
- **Badge or Pill:** Badge is state; Pill is a filter or a tag people made.
- **ListItem, DataTable or DetailList:** many things in rows, many things compared in columns, one thing's fields.
- **Diagram or Flow:** Flow adds direction.
- **LogViewer, LogExplorer or Terminal:** one live log, many labeled streams, one command's output.
- **CopyField or ShareLink:** a one-time secret, or a link meant to be shared.
- **DropZone or AvatarEditor:** files to upload, or one profile photo to crop.
- **SettingsPage or ProfilePage:** the instance, or one person.
- **Combobox or TagInput:** one value or many.

## Recipes

- **A settings screen:** SettingsPage; one `section` per topic with a Fieldset; Switches for instant settings, a `form[data-section]` with Save and Discard for the rest; the danger zone last behind a type-to-confirm Dialog.
- **A filterable list:** DataTable with a `pb-toolbar` holding a QueryFilter (or Segmented for a few states), BulkActions for selected rows, Pagination below, EmptyState for no matches.
- **A dashboard:** Dashboard grid; up to four StatTiles, then the main Chart with a TimeRange in the page header, then Gauges and StorageBar, then a Timeline and the service list.
- **Sharing files:** DropZone (with its upload settings) feeding an UploadQueue, ShareDialog for the link's terms, Media or MediaList for what's shared.
- **A file manager:** Resizable split with FileBrowser and FilePreview; Toast with Undo after a delete.
- **An account page:** ProfilePage with AvatarEditor for the photo, a section form for the profile, session rows with sign-out.

## Every component

| Component | Use it for | Not for (use instead) |
| --- | --- | --- |
| AlertEmail | emails the app sends: failures, warnings, digests | anything shown in the app |
| ApiTokens | listing, creating and revoking API tokens, shown once | other secrets in a page (CopyField) |
| AppIcons | favicon, home screen icons, `theme-color` | in-app icons (Icons) |
| AppShell | the frame of every app: header, nav, main, footer | websites and docs (SiteHeader) |
| AutoSubmit | filters and range pickers that apply as they change | destructive or costly submits |
| Avatar | people: initials or a photo, with presence | editing a photo (AvatarEditor) |
| AvatarEditor | uploading and cropping a profile photo | general file uploads (DropZone) |
| Badge | an item's state as icon, color and word | tags and filters (Pill) |
| BarChart | amounts across a few categories | time series (Chart) |
| Breadcrumbs | where a page sits, with links back up | top-level pages |
| BulkActions | acting on selected rows | a single row's actions (Menu) |
| BusyRegion | a region whose content is being replaced | first loads (Loading skeletons) |
| Button | triggering an action | navigation (Link) |
| ButtonGroup | related actions on one object; `pb-toolbar` rows | exclusive choices (Segmented) |
| Card | the base surface; outline for pickable panels | nesting more than one level |
| Chart | lines over time | categories (BarChart) |
| ChartGround | charts on a ground with ranges and limits | more than one ink chart per page |
| Checkbox | independent options saved with a form | instant settings (Switch) |
| ChoiceCard | options that need a description, figure or icon | short options (Radio, Segmented) |
| CodeArea | editing configs and scripts | read-only diffs (DiffViewer) |
| CodeInput | digit codes for verification and 2FA | codes with letters (TextInput) |
| ColorPicker | picking a color from kit swatches or hex | native color popups |
| Combobox | one value from a long, searchable list | under ~10 options (Select), many values (TagInput) |
| CommandPalette | ⌘K jump to pages, hosts and actions | anything not reachable another way |
| CommentThread | conversations on a share, file or incident | event history (Timeline) |
| ConfirmAction | asking before an action, then doing it | non-destructive actions |
| ContextMenu | actions on the thing under the pointer | the only route to an action |
| CopyField | a value to copy; one-time secrets | values people edit, shared links (ShareLink) |
| CronBuilder | editing a schedule, said back in words | one-off dates (DatePicker) |
| Dashboard | the 12-column overview grid | settings or forms |
| DataTable | comparing many things across columns | a simple list (ListItem), one thing (DetailList) |
| DatePicker | dates and ranges with a styled calendar | times (TimePicker) |
| Density | compact spacing for dense desktop views | forms, settings, phones |
| DetailList | one thing's label and value pairs | many items with the same fields (DataTable) |
| Diagram | topology: nodes, zones, edges | direction of flow (Flow) |
| Dialog | confirming, type-to-confirm, forms that can't wait, drawers | stacking; small info (Popover) |
| DiffViewer | what changed between two config versions | editing (CodeArea) |
| Disclosure | optional and advanced detail, FAQs | required fields |
| DropZone | accepting files by drag or browse | a profile photo (AvatarEditor) |
| DualRange | a minimum and maximum | a single value (Range) |
| EmptyState | empty lists, no matches, failed pages | loading (Loading) |
| ErrorSummary | listing a form's errors at the top | forms of one or two fields |
| EventCalendar | scheduled work by day | past events (Timeline) |
| FileBrowser | browsing a folder's files | many videos (MediaList) |
| FilePreview | previewing the selected file beside the browser | a modal preview |
| Fieldset | a form section with its own Save and Discard | actions floating below the form |
| Flow | directed diagrams: which way things move | undirected topology (Diagram) |
| Gauge | one value against limits; donuts for parts of a whole | more than five parts (horizontal bars) |
| Heatmap | a value per day across up to a year | continuous series (Chart) |
| Histogram | distributions, such as response times | categories (BarChart) |
| Icons | glyphs that paint in `currentColor` | the only signal of a state |
| ImageViewer | a gallery that opens full screen | a single shared item (Media) |
| InputTypes | native input types in the field box | styled date and time popups (DatePicker, TimePicker) |
| JsonViewer | inspecting structured data read-only | editing (CodeArea) |
| KeyValue | env vars, headers, labels | free text |
| Link | going somewhere, underlined | actions (Button) |
| ListItem | a set of things, one per row | column comparisons (DataTable) |
| Loading | spinners and skeletons | waits under 300 ms |
| LogExplorer | logs from many labeled streams | one service's log (LogViewer) |
| LogViewer | one service's live log | labeled streams (LogExplorer), one command (Terminal) |
| MarkdownEditor | formatted notes, descriptions, comments | code (CodeArea) |
| Media | one shared item with actions | many videos (MediaList) |
| MediaList | browsing many videos | mixed files (FileBrowser) |
| MediaPlayer | all video and audio playback | native `controls` |
| MediaState | media still processing; hover teasers | the only signal on touch devices |
| Menu | a popover list of actions | exclusive choices (Segmented) |
| MobileNav | phone tab bars and action sheets | more than five top-level places |
| Notice | a problem on this page or form | app-wide state (SiteBanner), your own action (Toast) |
| Notifications | what happened while they were away | results of their action (Toast) |
| NotifySettings | which events go to which channel | single on/off settings (Switch) |
| Pagination | moving through long result sets | feeds (Load more) |
| Pill | filter chips and tags people create | status (Badge) |
| Popover | a few facts and up to two actions | decisions (Dialog), short labels (Tooltip) |
| PrintReport | documents meant to be printed | on-screen dashboards |
| Progress | known-end work (`progress`), measurements (`meter`) | unknown waits (Loading) |
| ProfilePage | one person's own account | instance settings (SettingsPage) |
| Prose | long-form reading text | app chrome |
| QRCode | phones opening a link or joining a network | more than 213 bytes |
| QueryFilter | typed filters like `status:down host:nas-*` | a few fixed states (Segmented) |
| Rack | hardware by rack unit | topology (Diagram) |
| Radio | 2–6 exclusive options with hints | toolbars (Segmented), more than six (Select) |
| Range | a rough position | exact values (number input) |
| Resizable | two panes with a draggable divider | fixed layouts |
| Scrollbars | themed thin scrollbars (automatic) | hiding the only way to scroll |
| Segmented | 2–5 short exclusive choices in a toolbar | actions (ButtonGroup), choices with hints (Radio) |
| Select | more than six fixed options | 2–6 with hints (Radio), searchable lists (Combobox) |
| SessionTimeout | warning before idle sign-out | general alerts |
| SettingsPage | app-wide settings with section nav | one person's account (ProfilePage) |
| ShareDialog | who can open a link and on what terms | one-time secrets (CopyField) |
| ShareLink | copy link and QR as one group | one-time values (CopyField) |
| Shortcuts | the keyboard shortcut sheet on `?` | shortcuts not in the palette |
| SignIn | sign-in, two-factor setup, code prompt | in-app dialogs |
| SiteBanner | one app-wide announcement above the header | one item's problem (Notice) |
| SiteHeader | websites and docs: brand, links, one call to action | apps with a sidebar (AppShell) |
| SkipLink | jumping past the header by keyboard | nothing; every app page needs one |
| Sortable | reordering items, with a keyboard path | drag-only reordering |
| Sparkline | a trend beside a number | showing values (print the number) |
| StatTile | a headline metric with its history | more than one hero per page |
| StatusPage | a public status page with uptime and incidents | internal dashboards (Dashboard) |
| Steps | progress through a fixed sequence | more than six steps, view switching (Tabs) |
| StorageBar | a pool's space across datasets | more than four segments |
| StorageRing | storage use in the app header | the only signal of usage |
| Switch | settings that apply at once | choices that need Save (Checkbox) |
| TableTools | big tables: columns, resize, density, edit, CSV | small tables (DataTable) |
| Tabs | views of one object | filters (Segmented) |
| TagInput | many short values as chips | one value (Combobox) |
| Terminal | a command's output and exit status | live logs (LogViewer) |
| TextInput | single-line text in the field pattern | codes (CodeInput), copy-only values (CopyField) |
| TextUtils | relative times, middle truncation, line clamp | truncating something people must type |
| ThemeSwitch | light, dark or system | per-component theming by hand |
| TimePicker | a time of day with a styled picker | dates (DatePicker) |
| TimeRange | time windows and auto-refresh for logs and metrics | single dates (DatePicker) |
| Timeline | a record of events by day | unread events for the user (Notifications) |
| Toast | the result of the person's own action, with Undo | page problems (Notice), the only copy of an error |
| Tooltip | short labels under six words | important information (Popover) |
| TreeView | nested datasets, folders, files | flat lists (ListItem) |
| UploadQueue | per-file upload progress, retry, cancel | the transfer itself (your code does it) |
| VirtualList | past about 2,000 rows | short lists (render normally) |
