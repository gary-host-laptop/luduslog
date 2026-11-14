# IGDB metadata

IGDB supplies titles, summaries, covers, release dates, genres, platforms,
companies, themes, game modes, screenshots, videos, alternative names,
collections, and categories. LudusLog stores fetched metadata locally and does
not call IGDB on page requests.

## Setup

1. Create an IGDB application and obtain a client id and secret.
2. Set `igdb_client_id` and `igdb_secret` in config, or
   `LUDUSLOG_IGDB_CLIENT_ID` / `LUDUSLOG_IGDB_SECRET`.
3. Run `luduslog igdb sync --limit 200`.

The sync authenticates with Twitch's client-credentials flow, requests the most
rated games, and upserts them by IGDB id. `--limit` is the total number to
import, not the API page size: the client fetches in pages of 500 and throttles
between pages. `--limit 10000` is about 20 requests. Use `--offset` to resume a
large sync after a failure. A 429 response is retried with backoff.

Existing rows keep their local slug. Updating an IGDB game also repoints its
platforms, release dates, and other associations rather than creating duplicate
platform rows. Manual seed games use negative IGDB ids, and fetching the same
slug through IGDB adopts that row instead of failing.

## Fetching one game

Use the on-demand commands when a title is not in the popular bulk catalog:

```sh
luduslog igdb fetch --id 1029
luduslog igdb fetch --slug celeste
luduslog igdb fetch --search "Celeste"
```

`--search` prints the matches and saves the best match. Fetch-on-demand saves
the game locally, so later searches use the local catalog. Administrators also
get an **Add from IGDB** section on an empty `/search` result and a **Refresh
from IGDB** button on game pages. Those actions use the same save/adopt logic as
the CLI.

The `release_dates` mapping includes the platform IGDB id, region, date, human
label, and release status. A game's franchise is filled from its first IGDB
collection.

## Attribution

IGDB requires attribution wherever its data is displayed. When IGDB is
configured, the site footer says "Game metadata by IGDB. Covers served from the
IGDB image CDN." Keep this visible, and do not strip image URLs or rehost covers
without checking IGDB's terms.
