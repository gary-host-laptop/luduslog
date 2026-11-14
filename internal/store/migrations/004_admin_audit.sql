-- Persistent audit trail for instance-setting changes and other administrative
-- actions. Moderation-specific actions continue to live in mod_actions.
CREATE TABLE admin_audit (
  id         INTEGER PRIMARY KEY,
  actor_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
  action     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX admin_audit_created ON admin_audit(created_at DESC);
