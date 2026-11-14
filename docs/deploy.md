# Deployment

## Binary

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o luduslog ./cmd/luduslog
```

Create a config from `config.example.toml`, set `base_url` to the public HTTPS
URL, and generate a long random `session_key`:

```sh
head -c 48 /dev/urandom | base64
```

Run migrations, create the admin, then start the service:

```sh
./luduslog migrate -config /etc/luduslog/config.toml
./luduslog admin create admin admin@example.com
./luduslog serve -config /etc/luduslog/config.toml
```

The database directory is created automatically. Back up with:

```sh
./luduslog backup /backups/luduslog-$(date +%F).db
```

`backup` uses `VACUUM INTO`, so it is safe while the server is running.

## Docker

```sh
docker compose up -d --build
```

The compose file stores the database in the `luduslog-data` volume and reads
configuration from environment variables. Change `LUDUSLOG_BASE_URL`,
`LUDUSLOG_SESSION_KEY`, and registration settings before exposing it.

## Reverse proxy

Federation requires a stable public HTTPS origin. A minimal Caddy config:

```
log.example.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:8080
}
```

nginx needs `proxy_set_header Host $host;` and should pass client IPs so login
rate limiting works. Set `LUDUSLOG_AUTHORIZED_FETCH=1` if you want remote
servers to sign fetch requests.

## Upgrades

Replace the binary, then run `luduslog migrate`. Migrations are applied in
filename order inside transactions and recorded in `schema_migrations`. Take a
backup before upgrading.

## Health and monitoring

`GET /healthz` returns `{"status":"ok"}` with HTTP 200, or 503 when the database
is unreachable. NodeInfo reports user and post counts for fediverse directories.
