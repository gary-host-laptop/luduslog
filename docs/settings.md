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

## Account lifecycle

Users can choose between two ways to leave:

- **Deactivate** (`POST /settings/deactivate`) is reversible. The profile
  disappears, sessions are signed out, and data is retained. An administrator
  can restore the account.
- **Permanently delete** (`POST /settings/delete`) is a hard delete. It requires
  the current password and typing the username, removes the user row and all
  owned data, and cannot be undone.

Permanent deletion removes logs, reviews, comments, lists and list entries,
library entries, statuses, likes, follows, blocks, mutes, sessions, email
tokens, covers, notifications, badges, saved filters, remote follows, and the
user's avatar and header uploads. Comments left on the deleted user's profile,
reviews or lists are removed together with notifications that pointed at them,
and cached game counters are refreshed for every affected game.

The last active administrator cannot be deleted. Administrators can delete other
accounts from `/admin/users`; moderators cannot. Admin-initiated deletions are
recorded in `admin_audit`. Administrators cannot delete their own account from
the admin page — self-service deletion in Settings remains the path for that.

Deletion is local. Remote ActivityPub servers may retain cached copies of content
they already received, and this instance does not send outbound `Delete`
activities for deleted accounts.

## Favourite games

Profiles show up to five explicit favourite games on the Overview tab. Favourites
are managed in Settings → Favourites and are separate from game likes: liking a
game no longer changes the profile favourites section. Users can search the
catalog, add and remove picks, and reorder them by drag-and-drop or with the
move up/down buttons when JavaScript is unavailable. The sixth pick is rejected.

## Quick ratings

The game page's library card has a full-width five-star, half-step rating
control that stores the existing 1–10 value. Hovering previews a rating and
clicking a star saves it immediately; the compact clear icon removes an
existing rating. Without JavaScript, a Save button is shown after selecting a
rating, and the clear icon still works. Rating a game marks it as played and
raises the play counter to at least one; clearing the rating leaves the counter
alone. A rating without a log creates a minimal played log, while an existing
log keeps its review, status and replay flag.
