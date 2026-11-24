CREATE TABLE activity_visibility (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  visibility TEXT NOT NULL DEFAULT 'everyone',
  PRIMARY KEY (user_id, kind)
);
