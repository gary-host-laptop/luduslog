ALTER TABLE users ADD COLUMN default_played_status TEXT NOT NULL DEFAULT 'played';

-- Normalize the played-status values the editor understands. Older imports
-- used a handful of free-form values; map the close ones and fall back to
-- played so no log becomes unreachable from the status filters.
UPDATE logs SET status = 'completed' WHERE lower(trim(status)) IN ('finished', 'complete', 'beaten');
UPDATE logs SET status = 'shelved' WHERE lower(trim(status)) IN ('paused', 'on hold', 'on_hold');
UPDATE logs SET status = 'abandoned' WHERE lower(trim(status)) IN ('dropped', 'abandon');
UPDATE logs SET status = 'replayed' WHERE lower(trim(status)) IN ('replay', 'second play');
UPDATE logs SET status = 'played'
WHERE status = '' OR lower(trim(status)) NOT IN ('played', 'completed', 'playing', 'backlog', 'wishlist', 'abandoned', 'retired', 'shelved', 'replayed');
