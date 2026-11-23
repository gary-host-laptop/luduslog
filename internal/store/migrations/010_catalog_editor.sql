-- Trusted catalog editors may create and correct game metadata without being
-- full administrators. Moderators and administrators already qualify through
-- their role, so this flag extends that permission to selected regular users.
ALTER TABLE users ADD COLUMN catalog_editor INTEGER NOT NULL DEFAULT 0;
