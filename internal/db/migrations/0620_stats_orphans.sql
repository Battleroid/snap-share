-- Item ids are never reused from 0320 on (AUTOINCREMENT). Ids freed before
-- that may still have stats rows (events written after the delete) or
-- notification overrides; start the sequence above all of them, then drop
-- the stats rows whose item is gone. items.SweepOrphans does the same
-- cleanup every hour.

INSERT INTO sqlite_sequence (name, seq)
  SELECT 'items', 0 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'items');
UPDATE sqlite_sequence SET seq = max(seq,
    coalesce((SELECT max(id) FROM items), 0),
    coalesce((SELECT max(item_id) FROM item_events), 0),
    coalesce((SELECT max(item_id) FROM item_stats_daily), 0),
    coalesce((SELECT max(item_id) FROM item_visitors), 0),
    coalesce((SELECT max(CAST(subject_id AS INTEGER)) FROM notify_overrides WHERE subject_type = 'item'), 0))
  WHERE name = 'items';

DELETE FROM item_events WHERE item_id NOT IN (SELECT id FROM items);
DELETE FROM item_stats_daily WHERE item_id NOT IN (SELECT id FROM items);
DELETE FROM item_visitors WHERE item_id NOT IN (SELECT id FROM items);
