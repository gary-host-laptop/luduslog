-- Canonical post layer for feed interactions. A post points at one local
-- content row so likes, boosts and comments survive edits to that content.
CREATE TABLE posts (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,
  subject_type TEXT NOT NULL,
  subject_id   INTEGER NOT NULL,
  visibility   TEXT NOT NULL DEFAULT 'public',
  body         TEXT NOT NULL DEFAULT '',
  url          TEXT NOT NULL DEFAULT '',
  ap_id        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  deleted_at   TEXT,
  UNIQUE(subject_type, subject_id)
);
CREATE INDEX posts_user ON posts(user_id, created_at DESC);
CREATE INDEX posts_ap ON posts(ap_id) WHERE ap_id <> '';

CREATE TABLE post_likes (
  post_id      INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  actor_key    TEXT NOT NULL,
  user_id      INTEGER REFERENCES users(id) ON DELETE CASCADE,
  remote_actor TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  PRIMARY KEY (post_id, actor_key)
);
CREATE INDEX post_likes_post ON post_likes(post_id);

CREATE TABLE post_boosts (
  post_id      INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  actor_key    TEXT NOT NULL,
  user_id      INTEGER REFERENCES users(id) ON DELETE CASCADE,
  remote_actor TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  PRIMARY KEY (post_id, actor_key)
);
CREATE INDEX post_boosts_post ON post_boosts(post_id);
CREATE INDEX post_boosts_user ON post_boosts(user_id, created_at DESC);

ALTER TABLE reports ADD COLUMN remote_reporter TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN source_instance TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN forwarded INTEGER NOT NULL DEFAULT 0;

-- Backfill reviews, journal logs, sessions and social activities. New content
-- is added by Store.EnsurePosts, so this is a best-effort seed for old rows.
INSERT OR IGNORE INTO posts(user_id,kind,subject_type,subject_id,visibility,body,url,ap_id,created_at)
SELECT l.user_id,
  CASE WHEN l.review<>'' THEN 'review' ELSE 'log' END,
  'log', l.id,
  CASE
    WHEN u.private_profile=1 OR l.draft=1 THEN 'private'
    WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id
      AND av.kind=CASE WHEN l.review<>'' THEN 'review' ELSE 'log' END),'everyone')='private' THEN 'private'
    WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id
      AND av.kind=CASE WHEN l.review<>'' THEN 'review' ELSE 'log' END),'everyone')='followers' THEN 'followers'
    ELSE 'public' END,
  l.review,
  CASE WHEN l.review<>'' THEN '/users/'||u.username||'/reviews/'||l.id
       ELSE '/users/'||u.username||'/logs/'||g.slug END,
  '', l.created_at
FROM logs l JOIN users u ON u.id=l.user_id JOIN games g ON g.id=l.game_id
WHERE l.deleted_at IS NULL AND l.auto_created=0;

INSERT OR IGNORE INTO posts(user_id,kind,subject_type,subject_id,visibility,body,url,ap_id,created_at)
SELECT l.user_id,'session','session',ps.id,
  CASE
    WHEN u.private_profile=1 OR l.draft=1 THEN 'private'
    WHEN ps.note_privacy IN ('none','private') THEN 'private'
    WHEN ps.note_privacy IN ('friends','') THEN 'followers'
    WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id AND av.kind='session'),'everyone')='private' THEN 'private'
    WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id AND av.kind='session'),'everyone')='followers' THEN 'followers'
    ELSE 'public' END,
  ps.note, '/logs/'||l.id||'/sessions', '', ps.created_at
FROM play_sessions ps JOIN logs l ON l.id=ps.log_id JOIN users u ON u.id=l.user_id
WHERE l.deleted_at IS NULL AND ps.note<>'';

INSERT OR IGNORE INTO posts(user_id,kind,subject_type,subject_id,visibility,body,url,ap_id,created_at)
SELECT a.user_id,
  CASE a.type WHEN 'status' THEN 'shelf' WHEN 'liked' THEN 'like' WHEN 'rated' THEN 'rating' WHEN 'list_add' THEN 'list' ELSE a.type END,
  'activity', a.id,
  CASE
    WHEN u.private_profile=1 THEN 'private'
    WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=a.user_id
      AND av.kind=CASE a.type WHEN 'status' THEN 'shelf' WHEN 'liked' THEN 'like' WHEN 'rated' THEN 'rating' WHEN 'list_add' THEN 'list' ELSE a.type END),'everyone')='private' THEN 'private'
    WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=a.user_id
      AND av.kind=CASE a.type WHEN 'status' THEN 'shelf' WHEN 'liked' THEN 'like' WHEN 'rated' THEN 'rating' WHEN 'list_add' THEN 'list' ELSE a.type END),'everyone')='followers' THEN 'followers'
    ELSE 'public' END,
  a.target_name,
  CASE WHEN a.list_id IS NOT NULL THEN '/users/'||u.username||'/lists/'||COALESCE((SELECT slug FROM lists WHERE id=a.list_id),'')
       WHEN a.game_id IS NOT NULL THEN '/games/'||COALESCE((SELECT slug FROM games WHERE id=a.game_id),'')
       ELSE '/users/'||u.username END,
  '', a.created_at
FROM activities a JOIN users u ON u.id=a.user_id
WHERE a.type IN ('status','liked','rated','list_add');
