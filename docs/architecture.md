# Architecture

LudusLog is deliberately a monolith: one binary, one SQLite database, no
runtime services. The code is split so that each layer can be read on its own.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/luduslog` | CLI entry point, migrations, seeding, backup/export, IGDB sync, delivery worker. |
| `internal/config` | Tiny TOML reader plus `LUDUSLOG_*` environment overrides. |
| `internal/models` | Plain structs shared by the store and web layers. No behaviour. |
| `internal/store` | All SQL. Migrations are embedded, queries are plain SQL, one file per domain. |
| `internal/auth` | Password hashing, sessions, rate limits, email tokens. |
| `internal/markdown` | Small safe Markdown renderer. Escapes first, then applies a fixed subset. |
| `internal/federation` | RSA keys, HTTP signatures, actor and activity parsing. |
| `internal/queue` | Database-backed jobs with claim, retry, and exponential backoff. |
| `internal/mail` | SMTP mail; logs messages when SMTP is not configured. |
| `internal/igdb` | Optional metadata client used by `igdb sync`. |
| `internal/web` | Handlers, middleware, templates, and embedded static assets. |

## Request flow

```
net/http.ServeMux
  → recoverer
  → security headers
  → request log
  → session / CSRF / flash cookies
  → CSRF protection on unsafe methods
  → handler
  → template execution ("layout" + page "content")
```

Sessions are opaque random tokens stored server-side. The CSRF token is both in
the session and in a readable double-submit cookie; forms include it as
`_csrf`, and htmx sends it as `X-CSRF-Token` from `app.js`.

## Rendering

Templates live in `internal/web/templates` and are embedded with `go:embed`.
Each page is parsed as a set: `layout.html`, `_partials.html`, and the page
file. `layout.html` defines `layout`; pages define `content`. Partial templates
(`game_card`, `log_row`, `pager`, …) are shared. This keeps every page parse
independent without a global template registry.

Progressive enhancement is layered on top: forms post normally, links are plain
links, and a small number of interactions use htmx. `app.js` adds the CSRF
header, a theme toggle, copy buttons, and spoiler toggles. It is optional.

## Data model

The schema lives in `internal/store/migrations/001_init.sql`. Highlights:

- `games` is the canonical catalog; `game_*` tables store platforms, genres,
  companies, releases, relations, editions, and bundle contents.
- `logs` is one row per user/game entry and carries the review, rating, status,
  and dates. `sessions` hangs off logs.
- `statuses` holds the library flags used by the library and counters.
- `lists`/`list_entries` support ordering, ranking, and folders.
- `users` carries privacy and federation preferences.
- FTS5 virtual tables and triggers index games, users, and lists for search.
- `jobs` and `deliveries` back the queue and outbound federation.

Aggregate counters on `games` are refreshed by `RefreshGameStats` after a log
change. The weighted score is a Bayesian average with a 3.6 prior and weight 25.

## Configuration and startup

`config.Load` applies defaults, optional TOML, then environment variables.
`serve` opens the database (creating its directory), applies embedded
migrations, starts queue workers, and serves HTTP. On `SIGINT`/`SIGTERM` it
shuts down gracefully. TLS can be terminated by the process or a proxy.
