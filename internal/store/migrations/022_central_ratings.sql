-- A rating is a game-level opinion, independent of logs, shelves and play counts.
CREATE TABLE game_ratings (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id    INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  rating     REAL NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id)
);
CREATE INDEX game_ratings_game_rating ON game_ratings(game_id, rating);

-- Prefer the log the old UI counted. When several were marked primary, take the
-- most recently updated one so the backfill is deterministic.
INSERT INTO game_ratings(user_id, game_id, rating, created_at, updated_at)
SELECT l.user_id, l.game_id, l.rating, l.created_at, l.updated_at
FROM logs l
WHERE l.rating > 0 AND l.deleted_at IS NULL AND l.draft = 0 AND l.primary_rating = 1
  AND l.id = (
    SELECT p.id FROM logs p
    WHERE p.user_id = l.user_id AND p.game_id = l.game_id AND p.rating > 0
      AND p.deleted_at IS NULL AND p.draft = 0 AND p.primary_rating = 1
    ORDER BY p.updated_at DESC, p.id DESC LIMIT 1
  );

-- Older logs sometimes had ratings but no primary marker. Use the newest of
-- those, still one rating per user and game.
INSERT INTO game_ratings(user_id, game_id, rating, created_at, updated_at)
SELECT l.user_id, l.game_id, l.rating, l.created_at, l.updated_at
FROM logs l
WHERE l.rating > 0 AND l.deleted_at IS NULL AND l.draft = 0
  AND NOT EXISTS (SELECT 1 FROM game_ratings gr WHERE gr.user_id = l.user_id AND gr.game_id = l.game_id)
  AND l.id = (
    SELECT n.id FROM logs n
    WHERE n.user_id = l.user_id AND n.game_id = l.game_id AND n.rating > 0
      AND n.deleted_at IS NULL AND n.draft = 0
    ORDER BY n.updated_at DESC, n.id DESC LIMIT 1
  );

-- The column is kept for one release so old exports still round-trip, but it
-- no longer decides which rating counts.
UPDATE logs SET primary_rating = 0;

UPDATE games SET
  avg_rating = COALESCE((SELECT AVG(gr.rating) FROM game_ratings gr WHERE gr.game_id = games.id AND gr.rating > 0), 0),
  rating_count = (SELECT COUNT(*) FROM game_ratings gr WHERE gr.game_id = games.id AND gr.rating > 0),
  weighted = CASE
    WHEN (SELECT COUNT(*) FROM game_ratings gr WHERE gr.game_id = games.id AND gr.rating > 0) > 0
    THEN ((SELECT AVG(gr.rating) FROM game_ratings gr WHERE gr.game_id = games.id AND gr.rating > 0) *
          (SELECT COUNT(*) FROM game_ratings gr WHERE gr.game_id = games.id AND gr.rating > 0) + 3.6 * 25) /
         ((SELECT COUNT(*) FROM game_ratings gr WHERE gr.game_id = games.id AND gr.rating > 0) + 25)
    ELSE 3.6
  END;
