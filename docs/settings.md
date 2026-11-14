# Instance settings

Administrators can edit a small allowlist of non-secret values at
`/admin/settings`. The values are stored in the `settings` table and overlaid
on the startup configuration for each request, so they take effect immediately.

Editable values:

- Instance name and description
- Registration mode and invite code
- Contact and admin email addresses
- Maximum comment length
- The remote-ratings federation default

Everything else stays in `config.toml` or `LUDUSLOG_*` environment variables.
This includes the session key, SMTP credentials, IGDB credentials, TLS
certificate/key paths, listen address, data directory, and database path. Those
values are never rendered by the settings page.

Role changes and instance-setting changes are recorded in `admin_audit`.
Moderation actions are recorded in `mod_actions` and shown at `/admin/modlog`.
Moderators can resolve reports, remove content, suspend users, and view the
moderation log. Only administrators can grant roles or change instance
settings. There is no paid supporter tier.

Use the config file and restart the service when changing any secret or
infrastructure value. The editable settings above do not require a restart.
