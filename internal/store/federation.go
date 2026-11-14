package store

import (
	"context"
	"database/sql"
	"time"

	"luduslog/internal/models"
)

// ActorKey returns the user's actor private key, generating one on first use.
func (s *Store) ActorKey(ctx context.Context, userID int64) (string, error) {
	var key string
	err := s.DB.QueryRowContext(ctx, `SELECT actor_key_pem FROM users WHERE id=?`, userID).Scan(&key)
	if err != nil {
		return "", err
	}
	return key, nil
}

func (s *Store) SetActorKey(ctx context.Context, userID int64, pem string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET actor_key_pem=? WHERE id=?`, pem, userID)
	return err
}

// RemoteActor is a cached remote ActivityPub actor.
type RemoteActor struct {
	ActorURI     string
	Domain       string
	Username     string
	Name         string
	Inbox        string
	SharedInbox  string
	Outbox       string
	KeyID        string
	PublicKeyPEM string
	Avatar       string
	Summary      string
	FetchedAt    time.Time
}

func (s *Store) RemoteActorByURI(ctx context.Context, uri string) (*RemoteActor, error) {
	var a RemoteActor
	var fetched string
	err := s.DB.QueryRowContext(ctx, `SELECT actor_uri,domain,preferred_username,name,inbox,shared_inbox,outbox,key_id,public_key_pem,avatar,summary,fetched_at
		FROM remote_actors WHERE actor_uri=?`, uri).Scan(&a.ActorURI, &a.Domain, &a.Username, &a.Name, &a.Inbox, &a.SharedInbox,
		&a.Outbox, &a.KeyID, &a.PublicKeyPEM, &a.Avatar, &a.Summary, &fetched)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.FetchedAt = parseTime(fetched)
	return &a, nil
}

func (s *Store) SaveRemoteActor(ctx context.Context, a *RemoteActor) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO remote_actors(actor_uri,domain,preferred_username,name,inbox,shared_inbox,outbox,key_id,public_key_pem,avatar,summary,fetched_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(actor_uri) DO UPDATE SET domain=excluded.domain,preferred_username=excluded.preferred_username,name=excluded.name,
		inbox=excluded.inbox,shared_inbox=excluded.shared_inbox,outbox=excluded.outbox,key_id=excluded.key_id,
		public_key_pem=excluded.public_key_pem,avatar=excluded.avatar,summary=excluded.summary,fetched_at=excluded.fetched_at`,
		a.ActorURI, a.Domain, a.Username, a.Name, a.Inbox, a.SharedInbox, a.Outbox, a.KeyID, a.PublicKeyPEM,
		a.Avatar, a.Summary, Now(), Now())
	return err
}

func (s *Store) InboxSeen(ctx context.Context, activityID string) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_activities WHERE activity_id=?`, activityID).Scan(&n)
	return n > 0
}

func (s *Store) MarkInboxSeen(ctx context.Context, activityID, actor, typ string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO inbox_activities(activity_id,actor,type,received_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`,
		activityID, actor, typ, Now())
	return err
}

// EnqueueDelivery stores an outbound ActivityPub delivery.
func (s *Store) EnqueueDelivery(ctx context.Context, activityID, inbox, payload string, attempts int) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO deliveries(activity_id,inbox,body,attempts,next_at,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		activityID, inbox, payload, attempts, Now(), Now())
	return err
}

type Delivery struct {
	ID         int64
	ActivityID string
	Inbox      string
	Payload    string
	Attempts   int
}

func (s *Store) DueDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,activity_id,inbox,body,attempts FROM deliveries
		WHERE done_at IS NULL AND next_at<=? ORDER BY next_at LIMIT ?`, Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		rows.Scan(&d.ID, &d.ActivityID, &d.Inbox, &d.Payload, &d.Attempts)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeliveryDone(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE deliveries SET done_at=? WHERE id=?`, Now(), id)
	return err
}

func (s *Store) DeliveryRetry(ctx context.Context, id int64, next time.Time, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE deliveries SET attempts=attempts+1,next_at=?,last_error=? WHERE id=?`,
		next.UTC().Format(time.RFC3339), errMsg, id)
	return err
}

// LocalFollowers returns approved local followers of a user.
func (s *Store) LocalFollowers(ctx context.Context, userID int64) []*models.User {
	users, _ := s.Followers(ctx, userID, 1000, 0)
	return users
}

func (s *Store) RemoteFollowers(ctx context.Context, userID int64) []RemoteActor {
	rows, err := s.DB.QueryContext(ctx, `SELECT actor_uri FROM remote_follows WHERE local_user_id=?`, userID)
	if err != nil {
		return nil
	}
	var uris []string
	for rows.Next() {
		var u string
		rows.Scan(&u)
		uris = append(uris, u)
	}
	rows.Close()
	var out []RemoteActor
	for _, uri := range uris {
		if a, err := s.RemoteActorByURI(ctx, uri); err == nil {
			out = append(out, *a)
		}
	}
	return out
}

// AddRemoteFollow records a remote actor following a local user.
func (s *Store) AddRemoteFollow(ctx context.Context, actorURI string, localUserID int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO remote_follows(actor_uri,local_user_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`,
		actorURI, localUserID, Now())
	return err
}

func (s *Store) RemoveRemoteFollow(ctx context.Context, actorURI string, localUserID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM remote_follows WHERE actor_uri=? AND local_user_id=?`, actorURI, localUserID)
	return err
}

// InsertRemoteNote stores a federated reply for display under a review.
func (s *Store) InsertRemoteNote(ctx context.Context, apID, actorURI, name, avatar, body, inReplyTo string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO posts_remote(ap_id,actor_uri,actor_name,actor_avatar,content_html,in_reply_to,published_at,fetched_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, apID, actorURI, name, avatar, body, inReplyTo, Now())
	return err
}

// RemoteNote is a cached inbound reply.
type RemoteNote struct {
	APID        string
	ActorURI    string
	ActorName   string
	ActorAvatar string
	Body        string
	InReplyTo   string
	CreatedAt   time.Time
}

func (s *Store) RemoteNotesFor(ctx context.Context, inReplyTo string) []RemoteNote {
	rows, err := s.DB.QueryContext(ctx, `SELECT ap_id,actor_uri,actor_name,actor_avatar,content_html,in_reply_to,published_at
		FROM posts_remote WHERE in_reply_to=? ORDER BY published_at`, inReplyTo)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []RemoteNote
	for rows.Next() {
		var n RemoteNote
		var created string
		rows.Scan(&n.APID, &n.ActorURI, &n.ActorName, &n.ActorAvatar, &n.Body, &n.InReplyTo, &created)
		n.CreatedAt = parseTime(created)
		out = append(out, n)
	}
	return out
}
