-- Platform names were not unique, so every import could add another copy of
-- "PC (Microsoft Windows)". Keep one row per case-insensitive name, preferring
-- the row that has an IGDB id, then repoint associations before deleting.

CREATE TEMP TABLE platform_keep AS
SELECT name COLLATE NOCASE AS name_key,
       COALESCE(MIN(CASE WHEN igdb_id IS NOT NULL THEN id END), MIN(id)) AS keep_id
FROM platforms
GROUP BY name COLLATE NOCASE;

UPDATE OR IGNORE game_platforms
SET platform_id = (
  SELECT pk.keep_id
  FROM platforms p JOIN platform_keep pk ON pk.name_key = p.name COLLATE NOCASE
  WHERE p.id = game_platforms.platform_id
)
WHERE platform_id IN (SELECT id FROM platforms);

DELETE FROM game_platforms WHERE platform_id NOT IN (SELECT id FROM platforms);

UPDATE game_release_dates
SET platform_id = (
  SELECT pk.keep_id
  FROM platforms p JOIN platform_keep pk ON pk.name_key = p.name COLLATE NOCASE
  WHERE p.id = game_release_dates.platform_id
)
WHERE platform_id IS NOT NULL;

DELETE FROM platforms WHERE id NOT IN (SELECT keep_id FROM platform_keep);
DROP TABLE platform_keep;

CREATE UNIQUE INDEX platforms_name_unique ON platforms(name COLLATE NOCASE);
