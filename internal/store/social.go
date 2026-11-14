package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"luduslog/internal/models"
)

// ---- comments ----

const commentCols = `c.id,c.user_id,c.subject_type,c.subject_id,c.parent_id,c.body,c.remote_actor,c.remote_name,
c.remote_avatar,c.deleted_at,c.created_at`

func scanComment(row scanner) (*models.Comment, error) {
	var c models.Comment
	var userID, parent sql.NullInt64
	var deleted sql.NullString
	var created string
	err := row.Scan(&c.ID, &userID, &c.SubjectType, &c.SubjectID, &parent, &c.Body, &c.RemoteActor, &c.RemoteName,
		&c.RemoteAvatar, &deleted, &created)
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		c.UserID = &userID.Int64
	}
	if parent.Valid {
		c.ParentID = &parent.Int64
	}
	c.Deleted = deleted.Valid
	c.CreatedAt = parseTime(created)
	c.Remote = c.RemoteActor != ""
	return &c, nil
}

func (s *Store) CommentsFor(ctx context.Context, subjectType string, subjectID int64) ([]*models.Comment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+commentCols+` FROM comments c
		WHERE c.subject_type=? AND c.subject_id=? ORDER BY c.created_at ASC`, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []*models.Comment
	byParent := map[int64][]*models.Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		if c.UserID != nil {
			c.User, _ = s.UserByID(ctx, *c.UserID)
		}
		if c.ParentID != nil {
			byParent[*c.ParentID] = append(byParent[*c.ParentID], c)
		} else {
			roots = append(roots, c)
		}
	}
	var attach func(c *models.Comment)
	attach = func(c *models.Comment) {
		c.Replies = byParent[c.ID]
		c.ReplyCount = len(c.Replies)
		for _, r := range c.Replies {
			attach(r)
			c.ReplyCount += r.ReplyCount
		}
	}
	for _, c := range roots {
		attach(c)
	}
	return roots, rows.Err()
}

func (s *Store) CreateComment(ctx context.Context, userID *int64, subjectType string, subjectID int64, parentID *int64, body, remoteActor, remoteName, remoteAvatar string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO comments(user_id,subject_type,subject_id,parent_id,body,remote_actor,remote_name,remote_avatar,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, userID, subjectType, subjectID, parentID, body, remoteActor, remoteName, remoteAvatar, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) CommentByID(ctx context.Context, id int64) (*models.Comment, error) {
	c, err := scanComment(s.DB.QueryRowContext(ctx, `SELECT `+commentCols+` FROM comments c WHERE c.id=?`, id))
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

func (s *Store) DeleteComment(ctx context.Context, id, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE comments SET deleted_at=? WHERE id=? AND user_id=?`, Now(), id, userID)
	return err
}

func (s *Store) ModDeleteComment(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE comments SET deleted_at=? WHERE id=?`, Now(), id)
	return err
}

func (s *Store) CountComments(ctx context.Context, subjectType string, subjectID int64) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE subject_type=? AND subject_id=? AND deleted_at IS NULL`, subjectType, subjectID).Scan(&n)
	return n
}

// ---- notifications ----

func (s *Store) Notify(ctx context.Context, userID int64, kind string, actorID *int64, objectType string, objectID int64, text, url string) error {
	if actorID != nil && *actorID == userID {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO notifications(user_id,type,actor_id,object_type,object_id,text,url,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, userID, kind, actorID, objectType, objectID, text, url, Now())
	return err
}

func (s *Store) Notifications(ctx context.Context, userID int64, limit, offset int) ([]*models.Notification, int) {
	var total, unread int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=?`, userID).Scan(&total)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=? AND read_at IS NULL`, userID).Scan(&unread)
	rows, err := s.DB.QueryContext(ctx, `SELECT id,user_id,type,actor_id,remote_actor,object_type,object_id,text,url,read_at,created_at
		FROM notifications WHERE user_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, unread
	}
	defer rows.Close()
	var out []*models.Notification
	for rows.Next() {
		n := &models.Notification{}
		var actorID sql.NullInt64
		var readAt sql.NullString
		var created string
		rows.Scan(&n.ID, &n.UserID, &n.Type, &actorID, &n.RemoteActor, &n.ObjectType, &n.ObjectID, &n.Text, &n.URL, &readAt, &created)
		if actorID.Valid {
			n.ActorID = &actorID.Int64
			n.Actor, _ = s.UserByID(ctx, actorID.Int64)
		}
		n.ReadAt = parseNullTime(readAt)
		n.CreatedAt = parseTime(created)
		out = append(out, n)
	}
	return out, unread
}

func (s *Store) MarkNotificationsRead(ctx context.Context, userID int64, id int64) error {
	if id > 0 {
		_, err := s.DB.ExecContext(ctx, `UPDATE notifications SET read_at=? WHERE id=? AND user_id=?`, Now(), id, userID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE notifications SET read_at=? WHERE user_id=? AND read_at IS NULL`, Now(), userID)
	return err
}

func (s *Store) UnreadNotifications(ctx context.Context, userID int64) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=? AND read_at IS NULL`, userID).Scan(&n)
	return n
}

// ---- activity feed ----

func (s *Store) AddActivity(ctx context.Context, userID int64, kind string, gameID, logID, listID *int64, targetName, visibility string) error {
	if visibility == "" {
		visibility = "public"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO activities(user_id,type,game_id,log_id,list_id,target_name,visibility,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, userID, kind, gameID, logID, listID, targetName, visibility, Now())
	return err
}

// ActivityFeed returns activity from the user and their approved follows.
func (s *Store) ActivityFeed(ctx context.Context, viewerID int64, profileID int64, limit, offset int, includeFriends bool) ([]models.Activity, error) {
	var userIDs []int64
	if profileID > 0 {
		userIDs = []int64{profileID}
	} else {
		userIDs = append(userIDs, viewerID)
		userIDs = append(userIDs, s.FriendIDs(ctx, viewerID)...)
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id,a.user_id,a.type,a.game_id,a.log_id,a.list_id,a.target_name,a.visibility,a.created_at
		FROM activities a WHERE a.user_id IN (`+placeholders(len(userIDs))+`) ORDER BY a.created_at DESC LIMIT ? OFFSET ?`,
		append(int64Args(userIDs), limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Activity
	for rows.Next() {
		var a models.Activity
		var game, logID, listID sql.NullInt64
		var created string
		rows.Scan(&a.ID, &a.UserID, &a.Type, &game, &logID, &listID, &a.TargetName, &a.Visibility, &created)
		if game.Valid {
			a.GameID = &game.Int64
			a.Game, _ = s.GameByID(ctx, game.Int64)
		}
		if logID.Valid {
			a.LogID = &logID.Int64
		}
		if listID.Valid {
			a.ListID = &listID.Int64
		}
		a.User, _ = s.UserByID(ctx, a.UserID)
		a.CreatedAt = parseTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

func (s *Store) UserActivities(ctx context.Context, userID int64, limit, offset int) ([]models.Activity, error) {
	return s.ActivityFeed(ctx, userID, userID, limit, offset, false)
}

// ---- stats ----

type UserStats struct {
	GamesPlayed   int
	GamesPlaying  int
	GamesBacklog  int
	GamesWishlist int
	TotalHours    float64
	AvgRating     float64
	Reviews       int
	Lists         int
	PerfectGames  int
	LongestGame   string
	LongestHours  float64
	TopGenre      string
	TopGenreCount int
	TopPlatform   string
	ThisYear      int
	RatingCounts  map[int]int
	MonthlyPlays  map[string]int
	TopRated      []*models.Log
}

func (s *Store) StatsFor(ctx context.Context, userID int64) (*UserStats, error) {
	st := &UserStats{RatingCounts: map[int]int{}, MonthlyPlays: map[string]int{}}
	s.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(played),0),COALESCE(SUM(playing),0),COALESCE(SUM(backlog),0),COALESCE(SUM(wishlist),0)
		FROM statuses WHERE user_id=?`, userID).Scan(&st.GamesPlayed, &st.GamesPlaying, &st.GamesBacklog, &st.GamesWishlist)
	s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(playtime),0) FROM logs WHERE user_id=? AND deleted_at IS NULL`, userID).Scan(&st.TotalHours)
	s.DB.QueryRowContext(ctx, `SELECT COALESCE(AVG(rating),0) FROM logs WHERE user_id=? AND rating>0 AND primary_rating=1 AND deleted_at IS NULL`, userID).Scan(&st.AvgRating)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE user_id=? AND review<>'' AND deleted_at IS NULL`, userID).Scan(&st.Reviews)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM lists WHERE user_id=? AND deleted_at IS NULL`, userID).Scan(&st.Lists)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE user_id=? AND mastered=1 AND deleted_at IS NULL`, userID).Scan(&st.PerfectGames)
	var longest sql.NullString
	s.DB.QueryRowContext(ctx, `SELECT g.title, MAX(l.playtime) FROM logs l JOIN games g ON g.id=l.game_id WHERE l.user_id=? AND l.deleted_at IS NULL GROUP BY g.id ORDER BY 2 DESC LIMIT 1`, userID).Scan(&longest, &st.LongestHours)
	st.LongestGame = longest.String
	var topGenre sql.NullString
	s.DB.QueryRowContext(ctx, `SELECT gg.genre, COUNT(*) n FROM statuses st2 JOIN game_genres gg ON gg.game_id=st2.game_id
		WHERE st2.user_id=? AND st2.played=1 GROUP BY gg.genre ORDER BY n DESC LIMIT 1`, userID).Scan(&topGenre, &st.TopGenreCount)
	st.TopGenre = topGenre.String
	var topPlatform sql.NullString
	var topN int
	s.DB.QueryRowContext(ctx, `SELECT p.name, COUNT(*) n FROM logs l JOIN platforms p ON p.id=l.platform_id
		WHERE l.user_id=? AND l.deleted_at IS NULL GROUP BY p.name ORDER BY n DESC LIMIT 1`, userID).Scan(&topPlatform, &topN)
	st.TopPlatform = topPlatform.String
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE user_id=? AND deleted_at IS NULL AND substr(COALESCE(finished_at,started_at,created_at),1,4)=?`, userID, time.Now().UTC().Format("2006")).Scan(&st.ThisYear)
	rows, _ := s.DB.QueryContext(ctx, `SELECT CAST(rating AS INTEGER),COUNT(*) FROM logs WHERE user_id=? AND rating>0 AND primary_rating=1 AND deleted_at IS NULL GROUP BY 1`, userID)
	if rows != nil {
		for rows.Next() {
			var r, n int
			rows.Scan(&r, &n)
			st.RatingCounts[r] += n
		}
		rows.Close()
	}
	mrows, _ := s.DB.QueryContext(ctx, `SELECT substr(COALESCE(l.finished_at,l.started_at,l.created_at),1,7),COUNT(*) FROM logs l WHERE l.user_id=? AND l.deleted_at IS NULL GROUP BY 1`, userID)
	if mrows != nil {
		for mrows.Next() {
			var m string
			var n int
			mrows.Scan(&m, &n)
			st.MonthlyPlays[m] = n
		}
		mrows.Close()
	}
	trows, _ := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l WHERE l.user_id=? AND l.rating>0 AND l.deleted_at IS NULL ORDER BY l.rating DESC LIMIT 5`, userID)
	if trows != nil {
		for trows.Next() {
			l, err := scanLog(trows)
			if err == nil {
				st.TopRated = append(st.TopRated, l)
			}
		}
		trows.Close()
	}
	// Hydration issues its own queries, so it must happen after the rows are
	// released or a single-connection pool deadlocks.
	for _, l := range st.TopRated {
		s.hydrateLog(ctx, l)
	}
	return st, nil
}

// ---- badges ----

var badgeDefs = []models.Badge{
	{Key: "first_log", Title: "First Steps", Desc: "Log your first game."},
	{Key: "first_review", Title: "Critic", Desc: "Publish your first review."},
	{Key: "ten_games", Title: "Dedicated", Desc: "Play 10 games."},
	{Key: "fifty_games", Title: "Marathoner", Desc: "Play 50 games."},
	{Key: "hundred_games", Title: "Completionist", Desc: "Play 100 games."},
	{Key: "first_list", Title: "Curator", Desc: "Create a list."},
	{Key: "five_lists", Title: "Archivist", Desc: "Create five lists."},
	{Key: "ten_reviews", Title: "Wordsmith", Desc: "Publish ten reviews."},
	{Key: "perfect", Title: "Master", Desc: "Master a game."},
	{Key: "year_in_review", Title: "Recap", Desc: "View your year in review."},
}

func (s *Store) BadgesFor(ctx context.Context, userID int64) ([]models.Badge, error) {
	earned := map[string]time.Time{}
	rows, err := s.DB.QueryContext(ctx, `SELECT badge_key,earned_at FROM user_badges WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, at string
		rows.Scan(&k, &at)
		earned[k] = parseTime(at)
	}
	rows.Close()
	var out []models.Badge
	for _, b := range badgeDefs {
		if at, ok := earned[b.Key]; ok {
			t := at
			b.EarnedAt = &t
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *Store) AwardBadge(ctx context.Context, userID int64, key string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO user_badges(user_id,badge_key,earned_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, userID, key, Now())
	return err
}

// CheckBadges awards any badges the user now qualifies for.
func (s *Store) CheckBadges(ctx context.Context, userID int64) {
	checks := map[string]string{
		"first_log":     `SELECT COUNT(*) FROM logs WHERE user_id=? AND deleted_at IS NULL`,
		"first_review":  `SELECT COUNT(*) FROM logs WHERE user_id=? AND review<>'' AND deleted_at IS NULL`,
		"ten_reviews":   `SELECT COUNT(*) FROM logs WHERE user_id=? AND review<>'' AND deleted_at IS NULL`,
		"ten_games":     `SELECT COALESCE(SUM(played),0) FROM statuses WHERE user_id=?`,
		"fifty_games":   `SELECT COALESCE(SUM(played),0) FROM statuses WHERE user_id=?`,
		"hundred_games": `SELECT COALESCE(SUM(played),0) FROM statuses WHERE user_id=?`,
		"first_list":    `SELECT COUNT(*) FROM lists WHERE user_id=? AND deleted_at IS NULL`,
		"five_lists":    `SELECT COUNT(*) FROM lists WHERE user_id=? AND deleted_at IS NULL`,
		"perfect":       `SELECT COUNT(*) FROM logs WHERE user_id=? AND mastered=1 AND deleted_at IS NULL`,
	}
	thresholds := map[string]int{"first_log": 1, "first_review": 1, "ten_reviews": 10, "ten_games": 10, "fifty_games": 50, "hundred_games": 100, "first_list": 1, "five_lists": 5, "perfect": 1}
	for key, q := range checks {
		var n int
		if err := s.DB.QueryRowContext(ctx, q, userID).Scan(&n); err == nil && n >= thresholds[key] {
			s.AwardBadge(ctx, userID, key)
		}
	}
}

// ---- reports & moderation ----

func (s *Store) CreateReport(ctx context.Context, reporterID *int64, targetType string, targetID int64, targetURL, reason, details string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO reports(reporter_id,target_type,target_id,target_url,reason,details,status,created_at)
		VALUES(?,?,?,?,?,?,'open',?)`, reporterID, targetType, targetID, targetURL, reason, details, Now())
	return err
}

func (s *Store) Reports(ctx context.Context, status string) ([]*models.Report, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,reporter_id,target_type,target_id,target_url,reason,details,status,created_at
		FROM reports WHERE (?='' OR status=?) ORDER BY created_at DESC`, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Report
	for rows.Next() {
		r := &models.Report{}
		var reporter sql.NullInt64
		var created string
		rows.Scan(&r.ID, &reporter, &r.TargetType, &r.TargetID, &r.TargetURL, &r.Reason, &r.Details, &r.Status, &created)
		if reporter.Valid {
			r.ReporterID = &reporter.Int64
			if u, err := s.UserByID(ctx, reporter.Int64); err == nil {
				r.Reporter = u.Username
			}
		}
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ResolveReport(ctx context.Context, id int64, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE reports SET status=? WHERE id=?`, status, id)
	return err
}

// ---- lookups used across pages ----

func (s *Store) RecentReviews(ctx context.Context, limit int) ([]*models.Log, error) {
	out, _, err := s.ReviewsFeed(ctx, 0, "all", 0, limit, 0)
	return out, err
}

func (s *Store) ReviewsForUser(ctx context.Context, userID int64, limit int) ([]*models.Log, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l WHERE l.user_id=? AND l.review<>'' AND l.deleted_at IS NULL
		ORDER BY l.updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	var out []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err == nil {
			out = append(out, l)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, l := range out {
		s.hydrateLog(ctx, l)
	}
	return out, nil
}

func (s *Store) CountReviews(ctx context.Context, userID int64) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE user_id=? AND review<>'' AND deleted_at IS NULL`, userID).Scan(&n)
	return n
}

func (s *Store) YearRecap(ctx context.Context, userID int64, year int) (map[string]any, []*models.Log, error) {
	out := map[string]any{}
	var games, hours float64
	var reviews int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT game_id),COALESCE(SUM(playtime),0),COALESCE(SUM(CASE WHEN review<>'' THEN 1 ELSE 0 END),0)
		FROM logs WHERE user_id=? AND deleted_at IS NULL AND substr(COALESCE(finished_at,started_at,created_at),1,4)=?`, userID, itoa(year)).Scan(&games, &hours, &reviews)
	out["games"] = int(games)
	out["hours"] = hours
	out["reviews"] = reviews
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l WHERE l.user_id=? AND l.deleted_at IS NULL AND substr(COALESCE(l.finished_at,l.started_at,l.created_at),1,4)=? ORDER BY l.rating DESC`, userID, itoa(year))
	if err != nil {
		return out, nil, err
	}
	defer rows.Close()
	var logs []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err == nil {
			s.hydrateLog(ctx, l)
			logs = append(logs, l)
		}
	}
	return out, logs, nil
}

func (s *Store) HasAnyLogs(ctx context.Context, userID int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE user_id=? AND deleted_at IS NULL`, userID).Scan(&n)
	return n > 0
}

func (s *Store) MentionUsers(ctx context.Context, body string, limit int) []*models.User {
	var out []*models.User
	words := strings.Fields(body)
	seen := map[string]bool{}
	for _, w := range words {
		if !strings.HasPrefix(w, "@") {
			continue
		}
		name := strings.Trim(w[1:], ".,!?):;\"'")
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		if u, err := s.UserByUsername(ctx, name); err == nil && len(out) < limit {
			out = append(out, u)
		}
	}
	return out
}

// BlockDomain adds a server-wide federation block.
func (s *Store) BlockDomain(ctx context.Context, domain, reason string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO domain_blocks(domain,reason,created_at) VALUES(?,?,?) ON CONFLICT(domain) DO UPDATE SET reason=excluded.reason`, domain, reason, Now())
	return err
}

// UnblockDomain removes a federation block.
func (s *Store) UnblockDomain(ctx context.Context, domain string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM domain_blocks WHERE domain=?`, domain)
	return err
}

// IsDomainBlocked reports whether federation with a host is disabled.
func (s *Store) IsDomainBlocked(ctx context.Context, domain string) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_blocks WHERE domain=?`, domain).Scan(&n)
	return n > 0
}
