Density sets how tight controls, rows and panels are.

- `data-density="compact"` on `<html>` or any container shrinks control height to 30px (small 24px), table rows to 32px, list rows to 40px, and trims card, fieldset, tile and form spacing. Comfortable (the default) needs no attribute.
- Use compact for dense admin tables, dashboards and log views on desktop; keep comfortable for forms, settings and anything used on phones, where 44px touch targets matter.
- TableTools has its own density switch that sets `data-density` on the table only.
- Keep one density per view; don't mix compact and comfortable panels side by side.
