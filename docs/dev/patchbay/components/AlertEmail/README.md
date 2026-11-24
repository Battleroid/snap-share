The alert email is the template for messages apps send: failures, warnings, digests.

## Markup

- File: `email/alert.html` in the export. Tables and inline styles only (no CSS variables, no web fonts, no flexbox), so it renders in Gmail, Outlook and Apple Mail; Georgia and Courier New stand in for the kit fonts. The inline styles belong to the email: mail clients ignore external CSS and no page CSP applies to a message. Do not copy them into app pages.
- Structure: a hidden preheader (the one-line summary inbox lists show), the app name, a status band, a title that says what happened, a sentence on what it means and what happens next, a details table (host, job, times), the error text in a code block, one button and one link, and a footer saying why they got it with a link to change it.
- Max width 560px; every link absolute.

## States

- The shipped template is the failure variant: a red band reading `FAILED · BACKUP`. For a warning use `#8a5200` on `#fff1c2` with dark text; for ok use green.
- A `prefers-color-scheme` block darkens the message in clients that support it.

## Keys

Links and the button are the only controls; give each link text that names its destination.

## Events

None; the component fires no events of its own.

## Do and don't

- Do: say what happened, what it means and what happens next.
- Do: keep every link absolute.
- Don't: put the only copy of an error in a subject line.
