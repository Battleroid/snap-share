Density sets how tight controls, rows and panels are.

## Markup

- `data-density="compact"` on `<html>` or any container. Comfortable (the default) needs no attribute.
- Comfortable: controls are 36 px (small 28 px) and table cells 44 px high. Compact: controls 30 px (small 24 px), table rows 32 px (header row 30 px), list rows at least 40 px, badges and pills 20 px, and card, fieldset, tile, form and dashboard spacing is trimmed.
- There is no touch density: nothing in either setting makes a control 44 px tall.
- TableTools has its own density switch that sets `data-density` on its own `.pb-dt` element only.

## States

- Two values: comfortable (no attribute) and `compact`. The attribute applies to everything inside the element that carries it.

## Keys

- None; density changes size and spacing only.

## Events

- None; the component fires no events of its own.

## Do and don't

- Do: use compact for dense admin tables, dashboards and log views on desktop.
- Do: keep comfortable for forms, settings and anything used on phones, where larger targets matter.
- Don't: mix compact and comfortable panels side by side; keep one density per view.
