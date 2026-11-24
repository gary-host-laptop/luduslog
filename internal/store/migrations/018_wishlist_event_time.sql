-- Track when a game entered the wishlist so "Recently anticipated" can rank
-- by recent interest rather than lifetime wishlist totals.
ALTER TABLE statuses ADD COLUMN wishlist_at TEXT;

UPDATE statuses SET wishlist_at = updated_at WHERE wishlist = 1;
