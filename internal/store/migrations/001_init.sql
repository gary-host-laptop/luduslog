-- LudusLog initial schema.
-- SQLite in WAL mode. All timestamps are ISO-8601 UTC strings. Date-only
-- values are YYYY-MM-DD. Public identifiers (slugs/usernames) are stable.

CREATE TABLE users (
  id                    INTEGER PRIMARY KEY,
  username              TEXT NOT NULL,
  username_lower        TEXT NOT NULL UNIQUE,
  email                 TEXT NOT NULL,
  email_lower           TEXT NOT NULL UNIQUE,
  email_verified        INTEGER NOT NULL DEFAULT 0,
  approved              INTEGER NOT NULL DEFAULT 1,
  password_hash         TEXT NOT NULL,
  bio                   TEXT NOT NULL DEFAULT '',
  avatar                TEXT NOT NULL DEFAULT '',
  header                TEXT NOT NULL DEFAULT '',
  is_admin              INTEGER NOT NULL DEFAULT 0,
  is_supporter          INTEGER NOT NULL DEFAULT 0,
  created_at            TEXT NOT NULL,
  deactivated_at        TEXT,
  last_username_change  TEXT,
  private_profile       INTEGER NOT NULL DEFAULT 0,
  hide_activity         INTEGER NOT NULL DEFAULT 0,
  manually_approves     INTEGER NOT NULL DEFAULT 0,
  hide_social_graph     INTEGER NOT NULL DEFAULT 0,
  adult_optin           INTEGER NOT NULL DEFAULT 0,
  star_scale            INTEGER NOT NULL DEFAULT 5,
  cover_mode            TEXT NOT NULL DEFAULT 'any',
  time_display          TEXT NOT NULL DEFAULT 'always',
  infinite_scroll       INTEGER NOT NULL DEFAULT 1,
  theme                 TEXT NOT NULL DEFAULT 'system',
  high_contrast         INTEGER NOT NULL DEFAULT 0,
  timezone              TEXT NOT NULL DEFAULT 'UTC',
  fade_played           INTEGER NOT NULL DEFAULT 0,
  journal_view          TEXT NOT NULL DEFAULT 'calendar',
  comment_privacy       TEXT NOT NULL DEFAULT 'everyone',
  note_privacy          TEXT NOT NULL DEFAULT 'friends',
  federation            INTEGER NOT NULL DEFAULT 1,
  fed_statuses          INTEGER NOT NULL DEFAULT 0,
  fed_reviews           INTEGER NOT NULL DEFAULT 1,
  fed_lists             INTEGER NOT NULL DEFAULT 1,
  actor_key_pem         TEXT NOT NULL DEFAULT '',
  remote_actor          TEXT NOT NULL DEFAULT '',
  social_links          TEXT NOT NULL DEFAULT '[]',
  last_seen_at          TEXT
);
CREATE INDEX users_created ON users(created_at);

CREATE TABLE sessions (
  token_hash  TEXT PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf        TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL,
  user_agent  TEXT NOT NULL DEFAULT '',
  ip          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE email_tokens (
  token_hash TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL, -- verify | reset | deactivate
  email      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE follows (
  follower_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  followee_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  approved    INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (follower_id, followee_id)
);
CREATE INDEX follows_followee ON follows(followee_id);

CREATE TABLE blocks (
  blocker_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  blocked_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (blocker_id, blocked_id)
);

CREATE TABLE mutes (
  muter_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  muted_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (muter_id, muted_id)
);

CREATE TABLE platforms (
  id         INTEGER PRIMARY KEY,
  igdb_id    INTEGER UNIQUE,
  name       TEXT NOT NULL,
  abbrev     TEXT NOT NULL DEFAULT '',
  generation INTEGER NOT NULL DEFAULT 0,
  family     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE games (
  id             INTEGER PRIMARY KEY,
  igdb_id        INTEGER UNIQUE,
  slug           TEXT NOT NULL UNIQUE,
  title          TEXT NOT NULL,
  sort_title     TEXT NOT NULL,
  summary        TEXT NOT NULL DEFAULT '',
  cover_url      TEXT NOT NULL DEFAULT '',
  banner_url     TEXT NOT NULL DEFAULT '',
  release_date   TEXT,
  release_year   INTEGER,
  release_status TEXT NOT NULL DEFAULT 'released',
  category       TEXT NOT NULL DEFAULT 'main_game',
  parent_id      INTEGER REFERENCES games(id) ON DELETE SET NULL,
  edition_name   TEXT NOT NULL DEFAULT '',
  franchise      TEXT NOT NULL DEFAULT '',
  adult          INTEGER NOT NULL DEFAULT 0,
  restricted     INTEGER NOT NULL DEFAULT 0,
  locked         INTEGER NOT NULL DEFAULT 0,
  merge_target   INTEGER REFERENCES games(id) ON DELETE SET NULL,
  avg_rating     REAL NOT NULL DEFAULT 0,
  rating_count   INTEGER NOT NULL DEFAULT 0,
  weighted       REAL NOT NULL DEFAULT 0,
  popularity     INTEGER NOT NULL DEFAULT 0,
  plays          INTEGER NOT NULL DEFAULT 0,
  completions    INTEGER NOT NULL DEFAULT 0,
  backlogs       INTEGER NOT NULL DEFAULT 0,
  wishlists      INTEGER NOT NULL DEFAULT 0,
  playing        INTEGER NOT NULL DEFAULT 0,
  likes          INTEGER NOT NULL DEFAULT 0,
  lists_count    INTEGER NOT NULL DEFAULT 0,
  reviews        INTEGER NOT NULL DEFAULT 0,
  search_text    TEXT NOT NULL DEFAULT '',
  media          TEXT NOT NULL DEFAULT '{}',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  synced_at      TEXT
);
CREATE INDEX games_parent ON games(parent_id);
CREATE INDEX games_release ON games(release_year, release_date);
CREATE INDEX games_rating ON games(avg_rating);
CREATE INDEX games_weighted ON games(weighted);
CREATE INDEX games_popularity ON games(popularity DESC);
CREATE INDEX games_category ON games(category);
CREATE INDEX games_franchise ON games(franchise);

CREATE VIRTUAL TABLE games_fts USING fts5(search_text, content='games', content_rowid='id');
CREATE TRIGGER games_ai AFTER INSERT ON games BEGIN
  INSERT INTO games_fts(rowid, search_text) VALUES (new.id, new.search_text);
END;
CREATE TRIGGER games_ad AFTER DELETE ON games BEGIN
  INSERT INTO games_fts(games_fts, rowid, search_text) VALUES ('delete', old.id, old.search_text);
END;
CREATE TRIGGER games_au AFTER UPDATE OF search_text ON games BEGIN
  INSERT INTO games_fts(games_fts, rowid, search_text) VALUES ('delete', old.id, old.search_text);
  INSERT INTO games_fts(rowid, search_text) VALUES (new.id, new.search_text);
END;

CREATE TABLE game_platforms (
  game_id     INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  platform_id INTEGER NOT NULL REFERENCES platforms(id) ON DELETE CASCADE,
  PRIMARY KEY (game_id, platform_id)
);
CREATE TABLE game_genres (
  game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  genre   TEXT NOT NULL,
  PRIMARY KEY (game_id, genre)
);
CREATE TABLE game_themes (
  game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  theme   TEXT NOT NULL,
  PRIMARY KEY (game_id, theme)
);
CREATE TABLE game_modes (
  game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  mode    TEXT NOT NULL,
  PRIMARY KEY (game_id, mode)
);
CREATE TABLE game_companies (
  game_id  INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  name     TEXT NOT NULL,
  role     TEXT NOT NULL,
  PRIMARY KEY (game_id, name, role)
);
CREATE TABLE game_alt_names (
  game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  name    TEXT NOT NULL,
  PRIMARY KEY (game_id, name)
);
CREATE TABLE game_release_dates (
  game_id     INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  platform_id INTEGER,
  platform    TEXT NOT NULL DEFAULT '',
  region      TEXT NOT NULL DEFAULT '',
  date        TEXT,
  human       TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE game_relations (
  game_id   INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  related_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  kind      TEXT NOT NULL, -- bundle_content | bundle | related
  PRIMARY KEY (game_id, related_id, kind)
);

CREATE TABLE statuses (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id    INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  played     INTEGER NOT NULL DEFAULT 0,
  playing    INTEGER NOT NULL DEFAULT 0,
  backlog    INTEGER NOT NULL DEFAULT 0,
  wishlist   INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id)
);
CREATE INDEX statuses_game ON statuses(game_id);

CREATE TABLE game_likes (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id    INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id)
);

CREATE TABLE logs (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id         INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  name            TEXT NOT NULL DEFAULT '',
  rating          REAL NOT NULL DEFAULT 0,
  review          TEXT NOT NULL DEFAULT '',
  spoiler         INTEGER NOT NULL DEFAULT 0,
  status          TEXT NOT NULL DEFAULT 'played',
  replay          INTEGER NOT NULL DEFAULT 0,
  mastered        INTEGER NOT NULL DEFAULT 0,
  difficulty      TEXT NOT NULL DEFAULT '',
  platform_id     INTEGER,
  played_on_id    INTEGER,
  ownership       TEXT NOT NULL DEFAULT '',
  storefront      TEXT NOT NULL DEFAULT '',
  edition_game_id INTEGER,
  started_at      TEXT,
  finished_at     TEXT,
  time_to_finish  REAL NOT NULL DEFAULT 0,
  time_to_master  REAL NOT NULL DEFAULT 0,
  playtime        REAL NOT NULL DEFAULT 0,
  sync_sessions   INTEGER NOT NULL DEFAULT 0,
  primary_rating  INTEGER NOT NULL DEFAULT 1,
  language        TEXT NOT NULL DEFAULT '',
  draft           INTEGER NOT NULL DEFAULT 0,
  ap_id           TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  deleted_at      TEXT
);
CREATE INDEX logs_user ON logs(user_id, updated_at DESC);
CREATE INDEX logs_game ON logs(game_id);
CREATE INDEX logs_review ON logs(game_id, review);
CREATE UNIQUE INDEX logs_ap ON logs(ap_id) WHERE ap_id <> '';

CREATE TABLE log_likes (
  log_id     INTEGER NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (log_id, user_id)
);

CREATE TABLE play_sessions (
  id           INTEGER PRIMARY KEY,
  log_id       INTEGER NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
  note         TEXT NOT NULL DEFAULT '',
  note_privacy TEXT NOT NULL DEFAULT 'friends',
  playtime     REAL NOT NULL DEFAULT 0,
  started_on   TEXT,
  ended_on     TEXT,
  created_at   TEXT NOT NULL
);
CREATE INDEX play_sessions_log ON play_sessions(log_id);
CREATE TABLE play_session_days (
  session_id INTEGER NOT NULL REFERENCES play_sessions(id) ON DELETE CASCADE,
  day        TEXT NOT NULL,
  PRIMARY KEY (session_id, day)
);
CREATE INDEX session_days_day ON play_session_days(day);
CREATE TABLE play_session_tags (
  session_id INTEGER NOT NULL REFERENCES play_sessions(id) ON DELETE CASCADE,
  tag        TEXT NOT NULL,
  PRIMARY KEY (session_id, tag)
);

CREATE TABLE hidden_games (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id)
);

CREATE TABLE library_entries (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id    INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  platform_id INTEGER,
  ownership  TEXT NOT NULL DEFAULT 'digital',
  storefront TEXT NOT NULL DEFAULT '',
  edition    TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX library_user ON library_entries(user_id);
CREATE INDEX library_game ON library_entries(game_id);

CREATE TABLE covers (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  url     TEXT NOT NULL,
  PRIMARY KEY (user_id, game_id)
);

CREATE TABLE lists (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug         TEXT NOT NULL,
  title        TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  tags         TEXT NOT NULL DEFAULT '[]',
  privacy      TEXT NOT NULL DEFAULT 'public',
  ranked       INTEGER NOT NULL DEFAULT 0,
  progress     INTEGER NOT NULL DEFAULT 0,
  default_sort TEXT NOT NULL DEFAULT 'user',
  sort_dir     TEXT NOT NULL DEFAULT 'asc',
  fade_played  INTEGER NOT NULL DEFAULT 0,
  display_mode TEXT NOT NULL DEFAULT 'grid',
  immersive    INTEGER NOT NULL DEFAULT 0,
  deleted_at   TEXT,
  ap_id        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (user_id, slug)
);
CREATE INDEX lists_user ON lists(user_id);

CREATE TABLE list_entries (
  list_id  INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  game_id  INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  position INTEGER NOT NULL DEFAULT 0,
  note     TEXT NOT NULL DEFAULT '',
  added_at TEXT NOT NULL,
  PRIMARY KEY (list_id, game_id)
);
CREATE TABLE list_likes (
  list_id    INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (list_id, user_id)
);
CREATE TABLE list_folders (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  sort       TEXT NOT NULL DEFAULT 'user',
  pos        INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE list_folder_items (
  folder_id INTEGER NOT NULL REFERENCES list_folders(id) ON DELETE CASCADE,
  list_id   INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  PRIMARY KEY (folder_id, list_id)
);
CREATE VIRTUAL TABLE lists_fts USING fts5(title, description, content='lists', content_rowid='id');
CREATE TRIGGER lists_ai AFTER INSERT ON lists BEGIN
  INSERT INTO lists_fts(rowid, title, description) VALUES (new.id, new.title, new.description);
END;
CREATE TRIGGER lists_ad AFTER DELETE ON lists BEGIN
  INSERT INTO lists_fts(lists_fts, rowid, title, description) VALUES ('delete', old.id, old.title, old.description);
END;
CREATE TRIGGER lists_au AFTER UPDATE ON lists BEGIN
  INSERT INTO lists_fts(lists_fts, rowid, title, description) VALUES ('delete', old.id, old.title, old.description);
  INSERT INTO lists_fts(rowid, title, description) VALUES (new.id, new.title, new.description);
END;

CREATE TABLE comments (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER REFERENCES users(id) ON DELETE CASCADE,
  subject_type  TEXT NOT NULL, -- review | list | profile
  subject_id    INTEGER NOT NULL,
  parent_id     INTEGER REFERENCES comments(id) ON DELETE CASCADE,
  body          TEXT NOT NULL,
  deleted_at    TEXT,
  ap_id         TEXT NOT NULL DEFAULT '',
  in_reply_to   TEXT NOT NULL DEFAULT '',
  remote_actor  TEXT NOT NULL DEFAULT '',
  remote_name   TEXT NOT NULL DEFAULT '',
  remote_avatar TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);
CREATE INDEX comments_subject ON comments(subject_type, subject_id);
CREATE INDEX comments_parent ON comments(parent_id);

CREATE TABLE notifications (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type       TEXT NOT NULL,
  actor_id   INTEGER REFERENCES users(id) ON DELETE CASCADE,
  remote_actor TEXT NOT NULL DEFAULT '',
  object_type TEXT NOT NULL DEFAULT '',
  object_id  INTEGER NOT NULL DEFAULT 0,
  text       TEXT NOT NULL DEFAULT '',
  url        TEXT NOT NULL DEFAULT '',
  read_at    TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX notifications_user ON notifications(user_id, created_at DESC);

CREATE TABLE activities (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type        TEXT NOT NULL,
  game_id     INTEGER,
  log_id      INTEGER,
  list_id     INTEGER,
  target_user INTEGER,
  target_name TEXT NOT NULL DEFAULT '',
  visibility  TEXT NOT NULL DEFAULT 'public',
  created_at  TEXT NOT NULL
);
CREATE INDEX activities_user ON activities(user_id, created_at DESC);
CREATE INDEX activities_created ON activities(created_at DESC);

CREATE TABLE shares (
  code       TEXT PRIMARY KEY,
  kind       TEXT NOT NULL,
  target     TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE user_badges (
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  badge     TEXT NOT NULL,
  earned_at TEXT NOT NULL,
  PRIMARY KEY (user_id, badge)
);

CREATE TABLE saved_filters (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  scope      TEXT NOT NULL DEFAULT 'games',
  query      TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE posts_remote (
  id           INTEGER PRIMARY KEY,
  ap_id        TEXT NOT NULL UNIQUE,
  actor_uri    TEXT NOT NULL,
  actor_name   TEXT NOT NULL DEFAULT '',
  actor_avatar TEXT NOT NULL DEFAULT '',
  content_html TEXT NOT NULL DEFAULT '',
  game_igdb_id INTEGER NOT NULL DEFAULT 0,
  game_title   TEXT NOT NULL DEFAULT '',
  game_url     TEXT NOT NULL DEFAULT '',
  rating       REAL NOT NULL DEFAULT 0,
  in_reply_to  TEXT NOT NULL DEFAULT '',
  sensitive    INTEGER NOT NULL DEFAULT 0,
  published_at TEXT NOT NULL,
  fetched_at   TEXT NOT NULL
);

CREATE TABLE remote_actors (
  actor_uri          TEXT PRIMARY KEY,
  domain             TEXT NOT NULL,
  preferred_username TEXT NOT NULL DEFAULT '',
  name               TEXT NOT NULL DEFAULT '',
  inbox              TEXT NOT NULL DEFAULT '',
  shared_inbox       TEXT NOT NULL DEFAULT '',
  outbox             TEXT NOT NULL DEFAULT '',
  key_id             TEXT NOT NULL DEFAULT '',
  public_key_pem     TEXT NOT NULL DEFAULT '',
  avatar             TEXT NOT NULL DEFAULT '',
  summary            TEXT NOT NULL DEFAULT '',
  fetched_at         TEXT NOT NULL,
  next_fetch_at      TEXT
);
CREATE INDEX remote_actors_domain ON remote_actors(domain);

CREATE TABLE remote_follows (
  id              INTEGER PRIMARY KEY,
  actor_uri       TEXT NOT NULL,
  local_user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  follow_activity TEXT NOT NULL DEFAULT '',
  accepted        INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL,
  UNIQUE (actor_uri, local_user_id)
);

CREATE TABLE remote_following (
  id             INTEGER PRIMARY KEY,
  local_user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  actor_uri      TEXT NOT NULL,
  accepted       INTEGER NOT NULL DEFAULT 0,
  follow_activity TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  UNIQUE (local_user_id, actor_uri)
);

CREATE TABLE inbox_activities (
  activity_id TEXT PRIMARY KEY,
  actor       TEXT NOT NULL DEFAULT '',
  type        TEXT NOT NULL DEFAULT '',
  received_at TEXT NOT NULL
);

CREATE TABLE deliveries (
  id          INTEGER PRIMARY KEY,
  activity_id TEXT NOT NULL,
  inbox       TEXT NOT NULL,
  body        TEXT NOT NULL,
  attempts    INTEGER NOT NULL DEFAULT 0,
  next_at     TEXT NOT NULL,
  last_error  TEXT NOT NULL DEFAULT '',
  done_at     TEXT,
  created_at  TEXT NOT NULL,
  UNIQUE (activity_id, inbox)
);
CREATE INDEX deliveries_due ON deliveries(next_at) WHERE done_at IS NULL;

CREATE TABLE domain_blocks (
  domain     TEXT PRIMARY KEY,
  severity   TEXT NOT NULL DEFAULT 'suspend', -- suspend | silence | media
  reason     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE jobs (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL DEFAULT '{}',
  run_at     TEXT NOT NULL,
  attempts   INTEGER NOT NULL DEFAULT 0,
  locked_at  TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  done_at    TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX jobs_due ON jobs(run_at) WHERE done_at IS NULL;

CREATE TABLE reports (
  id          INTEGER PRIMARY KEY,
  reporter_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  target_type TEXT NOT NULL,
  target_id   INTEGER NOT NULL,
  target_url  TEXT NOT NULL DEFAULT '',
  reason      TEXT NOT NULL,
  details     TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'open', -- open | resolved | dismissed
  resolution  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);

CREATE TABLE mod_actions (
  id         INTEGER PRIMARY KEY,
  moderator  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  target_user INTEGER REFERENCES users(id) ON DELETE CASCADE,
  action     TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  report_id  INTEGER,
  created_at TEXT NOT NULL
);

CREATE TABLE infractions (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  action     TEXT NOT NULL,
  subject    TEXT NOT NULL DEFAULT '',
  reason     TEXT NOT NULL DEFAULT '',
  appeal     TEXT NOT NULL DEFAULT '',
  appeal_status TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE news (
  id         INTEGER PRIMARY KEY,
  title      TEXT NOT NULL,
  body       TEXT NOT NULL,
  published  INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);

CREATE TABLE feedback (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  feeling    TEXT NOT NULL DEFAULT '',
  comment    TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE igdb_cache (
  key        TEXT PRIMARY KEY,
  body       TEXT NOT NULL,
  fetched_at TEXT NOT NULL
);

CREATE TABLE rate_limits (
  key        TEXT NOT NULL,
  window_start TEXT NOT NULL,
  count      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (key)
);
