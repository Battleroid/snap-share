-- First time a chat or social preview bot fetched the item (stats "preview"
-- events). With first_opened_at still NULL it drives the "previewed" badge
-- without a join. Unix milliseconds UTC; NULL when never previewed.
ALTER TABLE items ADD COLUMN previewed_at INTEGER;
