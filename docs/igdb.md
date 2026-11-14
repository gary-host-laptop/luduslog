# IGDB metadata

IGDB supplies titles, summaries, covers, release dates, genres, platforms, and
companies. LudusLog stores fetched metadata locally and does not call IGDB on
page requests.

## Setup

1. Create an IGDB application and obtain a client id and secret.
2. Set `igdb_client_id` and `igdb_secret` in config, or
   `LUDUSLOG_IGDB_CLIENT_ID` / `LUDUSLOG_IGDB_SECRET`.
3. Run `luduslog igdb sync --limit 200`.

The sync authenticates with Twitch's client-credentials flow, requests the most
rated games, and upserts them by IGDB id. Existing rows keep their local slug.
Manual games use negative IGDB ids so they never collide with API records.

## Attribution

IGDB requires attribution wherever its data is displayed. When IGDB is
configured, the site footer says "Game metadata by IGDB. Covers served from the
IGDB image CDN." Keep this visible, and do not strip image URLs or rehost covers
without checking IGDB's terms.
