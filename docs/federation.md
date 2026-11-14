# Federation

LudusLog speaks enough ActivityPub to be followed, to publish reviews, and to
accept replies from other fediverse software. Federation is opt-in per user.

## Endpoints

| Path | Content |
| --- | --- |
| `/.well-known/webfinger?resource=acct:user@host` | JRD document pointing at the actor. |
| `/.well-known/nodeinfo` and `/nodeinfo/2.1` | NodeInfo discovery and document. |
| `/users/{name}` with `Accept: application/activity+json` | `Person` actor. |
| `/users/{name}/outbox` | Ordered collection of public review `Create` activities. |
| `/users/{name}/followers` | Followers collection (local and remote). |
| `/users/{name}/following` | Following collection. |
| `/users/{name}/inbox` | Signed inbox for `Follow`, `Undo`, `Create`, and `Like`. |
| `/games/{slug}/activity` | Game-aware collection of public reviews. |

Actors expose `preferredUsername`, `inbox`, `outbox`, `followers`, `following`,
`publicKey`, avatar/header images, profile links, and `manuallyApprovesFollowers`.
The key id is `{actor}#main-key`; RSA keys are generated on first actor fetch and
stored on the user row.

## Signing and verification

Outbound POSTs use the draft-cavage HTTP signature scheme (`(request-target)`,
`host`, `date`, `digest`, `content-type`), signed with the local user's key.
Inbound requests are verified against the remote actor's public key fetched over
HTTPS. Requests with a blocked domain are rejected before the key is fetched.
Bodies are limited to 1 MiB.

## Delivery

Accepted activities that need a response (for example `Accept` for a `Follow`)
are queued as `deliver` jobs. Workers load the signer's key, sign the POST, and
retry with exponential backoff through the generic queue. Destination inboxes
are recorded in `deliveries` for auditing. Follows from remote actors are
stored in `remote_follows`; remote replies are cached in `posts_remote`.

## Privacy

- A user must enable federation in settings before their actor is served.
- Drafts are never included in the outbox or game collections.
- Private profiles are not exposed through collections.
- Domain blocks (suspend/silence) apply at inbox verification and delivery.
- `AuthorizedFetch` is available as a configuration flag for locked-down
  instances.

## Known limitations

- Delivery is at-least-once; non-2xx responses are retried by the queue.
- Incoming `Announce`/relay traffic and shared inboxes are not implemented.
- Remote replies are displayed but do not generate local notifications yet.
- ActivityPub objects are JSON maps rather than a fully typed model.
