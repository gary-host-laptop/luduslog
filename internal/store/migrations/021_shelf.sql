-- Replace the independent shelf booleans with one authoritative shelf value.
-- The played column stays as the times-played counter.
ALTER TABLE statuses ADD COLUMN shelf TEXT NOT NULL DEFAULT '';
ALTER TABLE statuses ADD COLUMN shelf_at TEXT;
CREATE INDEX statuses_shelf ON statuses(game_id, shelf);

-- Priority follows current UI semantics: playing wins, then a recorded play,
-- then backlog, then wishlist. Legacy contradictory rows settle deterministically.
UPDATE statuses SET shelf='playing', shelf_at=COALESCE(playing_at,updated_at) WHERE playing=1;
UPDATE statuses SET shelf='played', shelf_at=COALESCE(played_at,updated_at) WHERE shelf='' AND played>0;
UPDATE statuses SET shelf='backlog', shelf_at=COALESCE(backlog_at,updated_at) WHERE shelf='' AND backlog=1;
UPDATE statuses SET shelf='wishlist', shelf_at=COALESCE(wishlist_at,updated_at) WHERE shelf='' AND wishlist=1;

-- Shelf flags were never real playthrough outcomes. Keep the log, reclassify it.
UPDATE logs SET status='played' WHERE status IN ('playing','backlog','wishlist');
