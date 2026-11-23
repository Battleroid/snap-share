The notification grid sets which events go to which channel.

- Markup: `.pb-tablewrap.pb-notify[data-notify]` with a Data table: the first column is the event (`pb-notify__ev`: `b` name, `small` hint on frequency), then one column per channel with a `pb-check` in each cell. Add a `colgroup` with `col.pb-notify__ch` per channel so the columns keep a fixed width. Header cells hold the channel names. The first body row, `tr.pb-notify__all` ("All events"), holds a checkbox `[data-col=N]` per channel that sets the whole column and shows mixed when some are on; the footer row has `[data-test=channel]` buttons.
- `pbNotify(el)` keeps header boxes in sync (checked, mixed or empty), fires `change`, and on Test shows a sending state, fires `test` and confirms with a toast.
- Say how often each event can fire in its hint. Put the loudest events first.
