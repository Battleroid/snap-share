Avatars stand for people: initials on a pop color, or a photo.

## Markup

- `span.pb-avatar[data-avatar][data-name="Full Name"]`; `pbAvatar(el)` writes initials and picks one of four pop colors from the name, so a person keeps their color (`data-c` overrides). Put an `img` inside for a photo. `pb-avatar--user` shows the generic user icon.
- Stack: `div.pb-avatars[aria-label]` overlaps them; end with `pb-avatar--more` ("+2").
- `pb-person` pairs an avatar with `pb-person__name` and `pb-person__meta`.

## States

- Sizes: `--sm` (24px), default (32px), `--lg` (44px), `--xl` (64px).
- Presence: add a `pb-dot[data-status]` inside; it is kept (the initials no longer wipe it) and its state joins the avatar's name ("Sam Okafor, ok").
- Soft avatars are round, hard ones square.

## Keys

Not focusable. The hook sets `role=img` and an `aria-label` from `data-name` (plus the dot's status), and a `title`.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: give every avatar a `data-name`.
- Don't: rely on the color alone to tell people apart.
