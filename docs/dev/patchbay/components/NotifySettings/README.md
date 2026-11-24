The notification grid sets which events go to which channel.

## Markup
- `.pb-tablewrap.pb-notify[data-notify]` with a Data table: the first column is the event (`pb-notify__ev`: `b` name, `small` hint on frequency), then one column per channel with a `pb-check` in each cell. Add a `colgroup` with `col.pb-notify__ch` per channel so the columns keep a fixed width. Header cells hold the channel names.
- The first body row, `tr.pb-notify__all` ("All events"), holds a checkbox `[data-col=N]` per channel that sets the whole column; the footer row has `[data-test=channel]` buttons.
- `pbNotify(el)` (the `notify` hook) wires it. It adds a hidden `[data-notify-status]` live region (`role="status"`) when you do not provide one.

## States
- The `All events` checkbox is checked, mixed or empty to match its column.
- A Test button waits ("Sending…", `aria-disabled`) until your code settles it; only one test runs at a time.

## Keys
- Native checkboxes and buttons: Tab and Space/Enter.
- The grid announces the test result (sent, or the error message) in its live region.

## Events
- `pb:change` bubbles with `{name, checked}` for a cell, or `{column, checked}` for a column box.
- `pb:test` with `{channel, button}` when a Test button is pressed. Settle it with `pb:saved` or `pb:error` (`detail.message`) dispatched on the grid. The library sends and fakes nothing.

## Do and don't
- Do: say how often each event can fire in its hint.
- Do: put the loudest events first.
- Don't: leave a Test button without settling it; it stays busy until `pb:saved` or `pb:error`.
