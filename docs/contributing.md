# Contributing

## Style

- Go 1.22, standard formatting (`gofmt`). Keep packages small and explicit.
- No cgo. SQLite is `modernc.org/sqlite`.
- No frontend build step, no CDN, no third-party CSS frameworks.
- All SQL belongs in `internal/store`; handlers never touch `*sql.DB` directly.
- Every feature must work without JavaScript. htmx is an enhancement.
- Escape first: user content goes through `internal/markdown` or is escaped by
  `html/template`. Do not add `template.HTML` around raw input.

## Adding a page

1. Add the handler in `internal/web`, using `s.page` and `s.render`.
2. Register the route in `routes.go`.
3. Add `templates/<name>.html` with `{{define "content"}}`.
4. Add queries to the appropriate `internal/store` file.

Templates are parsed per page, so a template name must match the route's page
name used in `s.render`.

## Tests

```sh
go test ./...
```

Tests should cover logic, not markup: weighted ratings, privacy decisions,
Markdown escaping, HTTP signatures, and HTTP status flows. Use
`store.OpenMemory` for database tests and `httptest` for web tests. If a test
opens rows and then queries inside the loop, collect first, close, then hydrate.

## Schema changes

Add a new numbered migration file under `internal/store/migrations`. Never edit
an applied migration. Migrations run in transactions at startup.

## Commits

Keep changes focused. Describe the why, not just the what. There is no CLA.
