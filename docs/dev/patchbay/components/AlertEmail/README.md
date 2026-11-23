The alert email is the template for messages apps send: failures, warnings, digests.

- File: `email/alert.html` in the export. Tables and inline styles only (no CSS variables, no web fonts, no flexbox), so it renders in Gmail, Outlook and Apple Mail; Georgia and Courier New stand in for the kit fonts. A `prefers-color-scheme` block darkens it in clients that support it.
- Structure: a hidden preheader (the one-line summary inbox lists show), the app name, a status band (`FAILED · BACKUP` on red, warn uses `#8a5200` on `#fff1c2` with dark text, ok uses green), a title that says what happened, a sentence on what it means and what happens next, a details table (host, job, times), the error text in a code block, one button and one link, and a footer saying why they got it with a link to change it.
- Keep the max width at 560px and every link absolute. Never put the only copy of an error in a subject line.
