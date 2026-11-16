CREATE TABLE favorite_games (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id    INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  position   INTEGER NOT NULL CHECK(position BETWEEN 1 AND 5),
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id),
  UNIQUE (user_id, position)
);
