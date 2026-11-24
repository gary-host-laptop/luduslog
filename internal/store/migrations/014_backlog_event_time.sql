-- Track when a game entered the backlog shelf. Without this, unrelated shelf
-- edits update updated_at and make old backlog games look recently added.
ALTER TABLE statuses ADD COLUMN backlog_at TEXT;

UPDATE statuses SET backlog_at = updated_at WHERE backlog = 1;
