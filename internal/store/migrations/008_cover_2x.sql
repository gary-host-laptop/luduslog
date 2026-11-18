UPDATE games
SET cover_url = REPLACE(cover_url, '/t_cover_big/', '/t_cover_big_2x/')
WHERE cover_url LIKE '%/t_cover_big/%';
