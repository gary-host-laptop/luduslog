-- Track when a game entered the played or playing shelf. These differ from
-- updated_at, which also moves when other shelf flags change.
ALTER TABLE statuses ADD COLUMN played_at TEXT;
ALTER TABLE statuses ADD COLUMN playing_at TEXT;

UPDATE statuses SET played_at = updated_at WHERE played > 0;
UPDATE statuses SET playing_at = updated_at WHERE playing = 1;
