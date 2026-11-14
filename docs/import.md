# Importing a Backloggd library

LudusLog imports Backloggd JSON and CSV exports. Backloggd game ids are IGDB
ids, so the importer resolves each entry directly instead of guessing from
titles. Configure IGDB credentials first if the export contains games missing
from the local catalog.

```sh
luduslog import backloggd backloggd-export.json --user alice
```

Use `--no-fetch` to import only games that already exist in the catalog. The
settings page accepts the same files, but does not fetch missing games during
the upload; run the command above for a full library.

## What is imported

| Backloggd | LudusLog |
| --- | --- |
| Played / Currently playing / Backlog / Wishlist | Shelf flags |
| Liked | Game like |
| Rating (0–10) | Log rating (stored in the user's scale) |
| Playthrough review and spoiler flag | Review and spoiler flag |
| Start and finish dates | Log dates |
| Total hours/minutes | Log playtime |
| Mastered / Replay | Log flags |

The most recent non-empty review is used. Multiple playthroughs are merged into
one log: the earliest start date, latest finish date, any mastered/replay flag,
and the aggregate playtime. Games with only a rating still get a log so the
rating is preserved without marking them played.

## Re-running

Imports are idempotent. Each entry updates the user's existing log for that game
and replaces their shelf flags, so running the import again after a new export
does not create duplicate logs.

Unmatched games are listed at the end of the command. These are usually entries
Backloggd has but IGDB has removed or does not consider games. They are skipped
without affecting the rest of the import.
