The profile page is where someone manages their own account: photo, name, sign-in, sessions and preferences.

## Markup

- It uses the SettingsPage layout. A `pb-pagehead`, then `.pb-settings` with `ul.pb-settings__nav[data-section-nav]` and `pb-settings__main` holding one `section[id]` per topic. Read SettingsPage first; the two pages use the same nav, setting rows and section forms.
- The first nav item can be `li.pb-profile__who`: a small avatar, the name and `@username · role`, so the person can see whose account this is. It hides under 900px, where the nav becomes a row.
- Profile: a `pb-fieldset` "Photo" holding the AvatarEditor field. Under it goes a `form[data-section]` "About you" with display name, username (with an `@` affix and the public URL in its help), email (with a `verified` badge), pronouns and a short bio with a counter. Save and Discard sit in the fieldset footer.
- Sign-in: `pb-setting` rows for password (opens a Dialog with the current and new password and a strength meter), two-factor (with an `on` badge) and passkeys.
- Sessions: `pb-setting.pb-session` rows with a device icon in the label, an IP address and the last time active in mono. The current session gets a `this device` badge instead of a button. The footer has the count and "Sign out everywhere else".
- Preferences: settings that apply as soon as they change, with no Save: Segmented for density, Selects for date format and time zone, a Switch for reduced motion.
- Delete account comes last: `pb-fieldset.pb-danger` with one `pb-btn--danger` that opens a type-to-confirm Dialog asking for the username.

## States

- The "About you" section starts clean, with Save and Discard disabled. Each section settles with `pb:saved` or `pb:error` (see Fieldset).
- The photo doesn't join the section form. It saves on its own from its dialog, so a half-edited bio never blocks a new photo, and the reverse.
- With a single session left, "Sign out everywhere else" is disabled.

## Keys

- The same as SettingsPage: a nav link scrolls to its section and moves focus to the `h2`.
- The password dialog opens with focus on the current password, and Esc cancels it.

## Events

- The page fires none of its own. The parts it uses do: `pb:avatar` (AvatarEditor), and `pb:saved` and `pb:error` on each section form.

## Do and don't

- Do: keep account settings here and instance settings on SettingsPage. Someone changing their own photo should never be one click from deleting the instance.
- Do: show the current session and mark it, so "Sign out everywhere else" is clear about what stays.
- Do: ask for the current password before changing the password or the email.
- Don't: put the photo inside the "About you" form. A file input there marks the form dirty and Discard can't put it back.
