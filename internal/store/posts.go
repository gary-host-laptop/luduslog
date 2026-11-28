package store

import (
	"context"
	"database/sql"

	"luduslog/internal/models"
)

const postCols = `p.id,p.user_id,p.kind,p.subject_type,p.subject_id,p.visibility,p.body,p.url,p.ap_id,p.created_at,p.deleted_at`

func scanPost(row scanner) (*models.Post, error) {
	var p models.Post
	var created string
	var deleted sql.NullString
	if err := row.Scan(&p.ID, &p.UserID, &p.Kind, &p.SubjectType, &p.SubjectID, &p.Visibility,
		&p.Body, &p.URL, &p.APID, &created, &deleted); err != nil {
		return nil, err
	}
	p.CreatedAt = parseTime(created)
	p.Deleted = deleted.Valid
	return &p, nil
}

func (s *Store) PostByID(ctx context.Context, id, viewerID int64) (*models.Post, error) {
	p, err := scanPost(s.DB.QueryRowContext(ctx, `SELECT `+postCols+` FROM posts p WHERE p.id=? AND p.deleted_at IS NULL`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydratePost(ctx, p, viewerID)
	return p, nil
}

func (s *Store) PostForSubject(ctx context.Context, subjectType string, subjectID, viewerID int64) (*models.Post, error) {
	p, err := scanPost(s.DB.QueryRowContext(ctx, `SELECT `+postCols+` FROM posts p
		WHERE p.subject_type=? AND p.subject_id=? AND p.deleted_at IS NULL`, subjectType, subjectID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydratePost(ctx, p, viewerID)
	return p, nil
}

func (s *Store) PostByAPID(ctx context.Context, apID string, viewerID int64) (*models.Post, error) {
	if apID == "" {
		return nil, ErrNotFound
	}
	p, err := scanPost(s.DB.QueryRowContext(ctx, `SELECT `+postCols+` FROM posts p WHERE p.ap_id=? AND p.deleted_at IS NULL`, apID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydratePost(ctx, p, viewerID)
	return p, nil
}

func (s *Store) hydratePost(ctx context.Context, p *models.Post, viewerID int64) {
	p.User, _ = s.UserByID(ctx, p.UserID)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_likes WHERE post_id=?`, p.ID).Scan(&p.Likes)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_boosts WHERE post_id=?`, p.ID).Scan(&p.Boosts)
	p.Comments = s.CountComments(ctx, "post", p.ID)
	if viewerID != 0 {
		key := "user:" + itoa64(viewerID)
		var n int
		s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_likes WHERE post_id=? AND actor_key=?`, p.ID, key).Scan(&n)
		p.Liked = n > 0
		s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM post_boosts WHERE post_id=? AND actor_key=?`, p.ID, key).Scan(&n)
		p.Boosted = n > 0
	}
	switch p.SubjectType {
	case "log":
		p.Log, _ = s.LogByID(ctx, p.SubjectID)
		if p.Log != nil {
			p.Game = p.Log.Game
		}
	case "session":
		if sess, err := s.SessionByID(ctx, p.SubjectID); err == nil {
			p.Session = sess
			p.Log, _ = s.LogByID(ctx, sess.LogID)
			if p.Log != nil {
				p.Game = p.Log.Game
			}
		}
	case "activity":
		var activityType string
		var gameID, listID sql.NullInt64
		var targetName string
		if err := s.DB.QueryRowContext(ctx, `SELECT type,game_id,list_id,target_name FROM activities WHERE id=?`, p.SubjectID).
			Scan(&activityType, &gameID, &listID, &targetName); err == nil {
			p.Kind = activityKind(activityType)
			p.Body = targetName
			if gameID.Valid {
				p.Game, _ = s.GameByID(ctx, gameID.Int64)
			}
			if listID.Valid {
				p.List, _ = s.ListByID(ctx, listID.Int64, false)
			}
		}
	}
}

func activityKind(activityType string) string {
	switch activityType {
	case "status":
		return "shelf"
	case "liked":
		return "like"
	case "rated":
		return "rating"
	case "list_add":
		return "list"
	case "follow":
		return "follow"
	}
	return activityType
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// EnsurePosts inserts or refreshes canonical posts for the supplied users'
// existing logs, sessions and social activities. It is idempotent and is
// called before timeline reads so old content is backfilled automatically.
func (s *Store) EnsurePosts(ctx context.Context, userIDs []int64) error {
	userIDs = uniqueIDs(userIDs)
	filter := ""
	var args []any
	if len(userIDs) > 0 {
		filter = " AND l.user_id IN (" + placeholders(len(userIDs)) + ")"
		args = int64Args(userIDs)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO posts(user_id,kind,subject_type,subject_id,visibility,body,url,ap_id,created_at)
		SELECT l.user_id, CASE WHEN l.review<>'' THEN 'review' ELSE 'log' END, 'log', l.id,
		CASE
			WHEN u.private_profile=1 OR l.draft=1 THEN 'private'
			WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id
				AND av.kind=CASE WHEN l.review<>'' THEN 'review' ELSE 'log' END),'everyone')='private' THEN 'private'
			WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id
				AND av.kind=CASE WHEN l.review<>'' THEN 'review' ELSE 'log' END),'everyone')='followers' THEN 'followers'
			ELSE 'public' END,
		l.review,
		CASE WHEN l.review<>'' THEN '/users/'||u.username||'/reviews/'||l.id
			ELSE '/users/'||u.username||'/logs/'||g.slug END,
		'', l.created_at
		FROM logs l JOIN users u ON u.id=l.user_id JOIN games g ON g.id=l.game_id
		WHERE l.deleted_at IS NULL AND l.auto_created=0`+filter+`
		ON CONFLICT(subject_type,subject_id) DO UPDATE SET kind=excluded.kind, visibility=excluded.visibility,
			body=excluded.body, url=excluded.url`, args...); err != nil {
		return err
	}

	sessionFilter := ""
	var sessionArgs []any
	if len(userIDs) > 0 {
		sessionFilter = " AND l.user_id IN (" + placeholders(len(userIDs)) + ")"
		sessionArgs = int64Args(userIDs)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO posts(user_id,kind,subject_type,subject_id,visibility,body,url,ap_id,created_at)
		SELECT l.user_id,'session','session',ps.id,
		CASE
			WHEN u.private_profile=1 OR l.draft=1 THEN 'private'
			WHEN ps.note_privacy IN ('none','private') THEN 'private'
			WHEN ps.note_privacy IN ('friends','') THEN 'followers'
			WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id AND av.kind='session'),'everyone')='private' THEN 'private'
			WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=l.user_id AND av.kind='session'),'everyone')='followers' THEN 'followers'
			ELSE 'public' END,
		ps.note, '/logs/'||l.id||'/sessions', '', ps.created_at
		FROM play_sessions ps JOIN logs l ON l.id=ps.log_id JOIN users u ON u.id=l.user_id
		WHERE l.deleted_at IS NULL AND ps.note<>''`+sessionFilter+`
		ON CONFLICT(subject_type,subject_id) DO UPDATE SET visibility=excluded.visibility,
			body=excluded.body, url=excluded.url`, sessionArgs...); err != nil {
		return err
	}

	activityFilter := ""
	var activityArgs []any
	if len(userIDs) > 0 {
		activityFilter = " AND a.user_id IN (" + placeholders(len(userIDs)) + ")"
		activityArgs = int64Args(userIDs)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO posts(user_id,kind,subject_type,subject_id,visibility,body,url,ap_id,created_at)
		SELECT a.user_id,
		CASE a.type WHEN 'status' THEN 'shelf' WHEN 'liked' THEN 'like' WHEN 'rated' THEN 'rating' WHEN 'list_add' THEN 'list' ELSE a.type END,
		'activity', a.id,
		CASE
			WHEN u.private_profile=1 THEN 'private'
			WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=a.user_id
				AND av.kind=CASE a.type WHEN 'status' THEN 'shelf' WHEN 'liked' THEN 'like' WHEN 'rated' THEN 'rating' WHEN 'list_add' THEN 'list' ELSE a.type END),'everyone')='private' THEN 'private'
			WHEN COALESCE((SELECT av.visibility FROM activity_visibility av WHERE av.user_id=a.user_id
				AND av.kind=CASE a.type WHEN 'status' THEN 'shelf' WHEN 'liked' THEN 'like' WHEN 'rated' THEN 'rating' WHEN 'list_add' THEN 'list' ELSE a.type END),'everyone')='followers' THEN 'followers'
			ELSE 'public' END,
		a.target_name,
		CASE WHEN a.list_id IS NOT NULL THEN '/users/'||u.username||'/lists/'||COALESCE((SELECT slug FROM lists WHERE id=a.list_id),'')
			WHEN a.game_id IS NOT NULL THEN '/games/'||COALESCE((SELECT slug FROM games WHERE id=a.game_id),'')
			ELSE '/users/'||u.username END,
		'', a.created_at
		FROM activities a JOIN users u ON u.id=a.user_id
		WHERE a.type IN ('status','liked','rated','list_add')`+activityFilter+`
		ON CONFLICT(subject_type,subject_id) DO UPDATE SET kind=excluded.kind, visibility=excluded.visibility,
			body=excluded.body, url=excluded.url`, activityArgs...)
	return err
}

// SetPostAPID stores a stable ActivityPub object id once the base URL is known.
func (s *Store) SetPostAPID(ctx context.Context, id int64, apID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE posts SET ap_id=? WHERE id=? AND ap_id=''`, apID, id)
	return err
}

func (s *Store) DeletePost(ctx context.Context, id, userID int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE posts SET deleted_at=? WHERE id=? AND user_id=? AND deleted_at IS NULL`, Now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TogglePostLike(ctx context.Context, postID, userID int64) (bool, error) {
	return s.togglePostReaction(ctx, "post_likes", postID, userID)
}

func (s *Store) TogglePostBoost(ctx context.Context, postID, userID int64) (bool, error) {
	return s.togglePostReaction(ctx, "post_boosts", postID, userID)
}

func (s *Store) togglePostReaction(ctx context.Context, table string, postID, userID int64) (bool, error) {
	key := "user:" + itoa64(userID)
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE post_id=? AND actor_key=?`, postID, key).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM `+table+` WHERE post_id=? AND actor_key=?`, postID, key)
		return false, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO `+table+`(post_id,actor_key,user_id,remote_actor,created_at)
		VALUES(?,?,?,'',?)`, postID, key, userID, Now())
	return true, err
}

func (s *Store) AddRemotePostReaction(ctx context.Context, table string, postID int64, actorURI string) error {
	if table != "post_likes" && table != "post_boosts" {
		return ErrNotFound
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO `+table+`(post_id,actor_key,remote_actor,created_at)
		VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, postID, actorURI, actorURI, Now())
	return err
}

func (s *Store) RemoveRemotePostReaction(ctx context.Context, table string, postID int64, actorURI string) error {
	if table != "post_likes" && table != "post_boosts" {
		return ErrNotFound
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM `+table+` WHERE post_id=? AND actor_key=?`, postID, actorURI)
	return err
}

func (s *Store) AddRemotePostLike(ctx context.Context, postID int64, actorURI string) error {
	return s.AddRemotePostReaction(ctx, "post_likes", postID, actorURI)
}

func (s *Store) RemoveRemotePostLike(ctx context.Context, postID int64, actorURI string) error {
	return s.RemoveRemotePostReaction(ctx, "post_likes", postID, actorURI)
}

func (s *Store) AddRemotePostBoost(ctx context.Context, postID int64, actorURI string) error {
	return s.AddRemotePostReaction(ctx, "post_boosts", postID, actorURI)
}

func (s *Store) RemoveRemotePostBoost(ctx context.Context, postID int64, actorURI string) error {
	return s.RemoveRemotePostReaction(ctx, "post_boosts", postID, actorURI)
}

// PostActors lists actor URLs for the likes or shares collection.
func (s *Store) PostActors(ctx context.Context, postID int64, table string) []string {
	if table != "post_likes" && table != "post_boosts" {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT COALESCE(u.username,''),remote_actor FROM `+table+`
		LEFT JOIN users u ON u.id=`+table+`.user_id WHERE post_id=? ORDER BY created_at`, postID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var username, remote string
		rows.Scan(&username, &remote)
		if remote != "" {
			out = append(out, remote)
		} else if username != "" {
			out = append(out, "/users/"+username)
		}
	}
	return out
}

// PostReplies returns the comment ids that make up the replies collection.
func (s *Store) PostReplies(ctx context.Context, postID int64) []int64 {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM comments WHERE subject_type='post' AND subject_id=? AND deleted_at IS NULL ORDER BY created_at`, postID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

// SetCommentAPID records the canonical ActivityPub id for a local comment.
func (s *Store) SetCommentAPID(ctx context.Context, id int64, apID, inReplyTo string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE comments SET ap_id=?,in_reply_to=? WHERE id=?`, apID, inReplyTo, id)
	return err
}

func (s *Store) CommentByAPID(ctx context.Context, apID string) (*models.Comment, error) {
	if apID == "" {
		return nil, ErrNotFound
	}
	c, err := scanComment(s.DB.QueryRowContext(ctx, `SELECT `+commentCols+` FROM comments c WHERE c.ap_id=?`, apID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.UserID != nil {
		c.User, _ = s.UserByID(ctx, *c.UserID)
	}
	return c, nil
}

func (s *Store) UpdateRemoteCommentBody(ctx context.Context, apID, body string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE comments SET body=? WHERE ap_id=? AND remote_actor<>'' AND deleted_at IS NULL`, body, apID)
	return err
}

func (s *Store) DeleteCommentByAPID(ctx context.Context, apID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE comments SET deleted_at=? WHERE ap_id=? AND deleted_at IS NULL`, Now(), apID)
	return err
}

// BoostItems returns local boosts by the supplied users, newest first.
func (s *Store) BoostItems(ctx context.Context, userIDs []int64, limit int) ([]models.TimelineItem, error) {
	userIDs = uniqueIDs(userIDs)
	if len(userIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT pb.post_id,pb.user_id,pb.created_at
		FROM post_boosts pb JOIN posts p ON p.id=pb.post_id AND p.deleted_at IS NULL
		WHERE pb.user_id IN (`+placeholders(len(userIDs))+`)
		ORDER BY pb.created_at DESC LIMIT ?`, append(int64Args(userIDs), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.TimelineItem
	for rows.Next() {
		var postID, userID int64
		var created string
		if err := rows.Scan(&postID, &userID, &created); err != nil {
			continue
		}
		pid := postID
		out = append(out, models.TimelineItem{
			Key:  "boost:" + itoa64(postID) + ":" + itoa64(userID) + ":" + created,
			Kind: "boost", At: parseTime(created), UserID: userID, PostID: &pid, PostSubjectType: "post",
		})
	}
	return out, rows.Err()
}
