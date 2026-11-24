Avatars stand for people: initials on a pop color, or a photo.

- Markup: `span.pb-avatar[data-avatar][data-name="Full Name"]`; `pbAvatar(el)` writes initials and picks one of four pop colors from the name, so a person keeps their color. Put an `img` inside for a photo. `pb-avatar--user` shows the generic user icon.
- Sizes: `--sm` (20px), default (28px), `--lg` (36px), `--xl` (56px). Add a `pb-dot[data-status]` inside for presence.
- Stack: `div.pb-avatars[aria-label]` overlaps them; end with `pb-avatar--more` ("+2").
- `pb-person` pairs an avatar with `pb-person__name` and `pb-person__meta`. Soft avatars are round, hard ones square.
- A `.pb-dot[data-status]` inside the avatar is kept (the initials no longer wipe it) and its state joins the avatar's name ("Sam Okafor, ok").
