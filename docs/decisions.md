# Decisions

Ambiguities in the specification and the choices made here.

## Single binary with embedded assets

Templates and static files are embedded. This removes a deploy step and makes
the release artifact self-contained. Per-page template parsing costs a little
startup time but keeps template names predictable.

## Hand-written Markdown instead of a library

The supported subset is small and rendering escapes HTML before applying markup.
This avoids goldmark plus bluemonday while keeping the output safe by
construction. Links are restricted to `http` and `https`.

## Ratings stored on a 0–10 scale

One unit is a half star. The UI divides by two for five-star display and can show
ten points for users who prefer that. Averages and histograms use the stored
scale.

## Likes are not statuses

`game_likes` and `log_likes` are separate tables rather than a generic status
row. This keeps counts cheap and avoids overloading the activity model.

## Library flags separate from logs

`statuses` holds current intent (playing, backlog, wishlist) while `logs` holds
diary entries. A user can log a game without changing their library, and vice
versa. Counters are derived from statuses.

## Played is a count, not a flag

`statuses.played` records how many times a user has played a game. The game
page shows it as a compact pill that links to the log editor; the log form owns
the counter, and completed/replayed log saves keep it at least one. Public
aggregates count distinct player rows (`played > 0`) rather than summing
completions. Playing, backlog, and wishlist remain boolean flags. Likes live in
`game_likes`, separate from statuses and play counts.

## Sessions and CSRF

Sessions are server-side opaque tokens; no signed-cookie state. CSRF uses both a
session value and a readable double-submit cookie, which lets htmx attach the
token from JavaScript without an extra endpoint.

## First user becomes admin

Registration is often the first thing an operator does. Making the first
registered account an administrator removes a bootstrap command, while the
`admin create` command remains for recovery and automation.

## Approval registration

`registration = "approval"` creates accounts with `approved = 0`; they can log
in but cannot interact until an admin approves them from the admin area.

## Federation is opt-in

A new account does not publish an actor until federation is enabled in settings.
This avoids surprising users and keeps private instances private by default.

## Game-aware ActivityPub extension

Reviews are `Note` objects with `attachment` pointing at the game page, a
`Hashtag` for the game, and non-standard `rating`/`ratingScale` properties. These
are additive and ignored by generic servers, while game-aware clients can use
them.

## IGDB metadata is cached

The API is contacted by explicit syncs, not per request. Games store their IGDB
id, metadata, and last sync time. Attribution appears in the footer when IGDB is
configured. Manual games use negative IGDB ids to keep the unique index without
pretending to be API records.

## SQLite and jobs

WAL mode, a busy timeout, and a small connection pool keep reads fast. Jobs are
rows with `run_at`, `attempts`, and `last_error`; workers claim due rows and
retry with exponential backoff. This keeps the deployment to one process.

## Deactivation stays soft; deletion is permanent

Deactivation remains the reversible option and retains data for an administrator
to restore. Account deletion is a hard delete in a single transaction so usernames
and emails are freed and no orphaned comments or notifications remain. Uploads are
removed after the commit as best-effort cleanup. The last active administrator is
protected, and remote federation cleanup is out of scope because other servers may
retain cached copies.

## Favourites are explicit picks

Profile favourites are stored in `favorite_games` with a position, capped at five,
rather than derived from game likes. Likes and favourites answer different
questions, and an ordered list lets users curate what appears first. Existing likes
are deliberately not migrated.

## Quick ratings are ratings, not reviews

The game-page star control writes the same 1–10 rating the log form uses. It reuses
the latest non-draft log when one exists so review text and status are untouched,
and creates a minimal played log only when needed. Clearing a rating never lowers
the play counter.
