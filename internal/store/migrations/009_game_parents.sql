-- Editions are imported before their parent may be in the catalog. Remember the
-- IGDB parent id so a later sync pass can connect the rows.
ALTER TABLE games ADD COLUMN parent_igdb_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX games_parent_igdb ON games(parent_igdb_id);
