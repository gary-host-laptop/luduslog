-- Replace the single administrator flag with an explicit role. Supporters were
-- never implemented and the free-instance model has no paid tier, so the dead
-- column is removed rather than reused.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user'
  CHECK (role IN ('user','mod','admin'));
UPDATE users SET role='admin' WHERE is_admin=1;
ALTER TABLE users DROP COLUMN is_supporter;
ALTER TABLE users DROP COLUMN is_admin;
CREATE INDEX users_role ON users(role);
