-- Users can highlight one earned trophy on their profile.
ALTER TABLE users ADD COLUMN featured_badge TEXT NOT NULL DEFAULT '';
