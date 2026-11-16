package store

import (
	"context"
	"database/sql"
	"strings"

	"luduslog/internal/models"
)

const logCols = `l.id,l.user_id,l.game_id,l.name,l.rating,l.review,l.spoiler,l.status,l.replay,l.mastered,
l.difficulty,l.platform_id,l.played_on_id,l.ownership,l.storefront,l.edition_game_id,COALESCE(l.started_at,''),
COALESCE(l.finished_at,''),l.time_to_finish,l.time_to_master,l.playtime,l.sync_sessions,l.primary_rating,
l.language,l.draft,l.created_at,l.updated_at`

func scanLog(row scanner) (*models.Log, error) {
	var l models.Log
	var spoiler, replay, mastered, sync, primary, draft int
	var platform, playedOn, edition sql.NullInt64
	var created, updated string
	err := row.Scan(&l.ID, &l.UserID, &l.GameID, &l.Name, &l.Rating, &l.Review, &spoiler, &l.Status, &replay,
		&mastered, &l.Difficulty, &platform, &playedOn, &l.Ownership, &l.Storefront, &edition, &l.StartedAt,
		&l.FinishedAt, &l.TimeToFinish, &l.TimeToMaster, &l.Playtime, &sync, &primary, &l.Language, &draft,
		&created, &updated)
	if err != nil {
		return nil, err
	}
	if platform.Valid {
		l.PlatformID = &platform.Int64
	}
	if playedOn.Valid {
		l.PlayedOnID = &playedOn.Int64
	}
	if edition.Valid {
		l.EditionGameID = &edition.Int64
	}
	l.Spoiler = spoiler == 1
	l.Replay = replay == 1
	l.Mastered = mastered == 1
	l.SyncSessions = sync == 1
	l.PrimaryRating = primary == 1
	l.Draft = draft == 1
	l.CreatedAt = parseTime(created)
	l.UpdatedAt = parseTime(updated)
	return &l, nil
}

func (s *Store) hydrateLog(ctx context.Context, l *models.Log) {
	if g, err := s.GameByID(ctx, l.GameID); err == nil {
		l.Game = g
	}
	if u, err := s.UserByID(ctx, l.UserID); err == nil {
		l.User = u
	}
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM log_likes WHERE log_id=?`, l.ID).Scan(&n)
	l.Likes = n
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE subject_type='log' AND subject_id=? AND deleted_at IS NULL`, l.ID).Scan(&n)
	l.CommentCount = n
}

func (s *Store) LogByID(ctx context.Context, id int64) (*models.Log, error) {
	l, err := scanLog(s.DB.QueryRowContext(ctx, `SELECT `+logCols+` FROM logs l WHERE l.id=? AND l.deleted_at IS NULL`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydrateLog(ctx, l)
	return l, nil
}

func (s *Store) ReviewByGameAndID(ctx context.Context, gameID, id int64) (*models.Log, error) {
	l, err := scanLog(s.DB.QueryRowContext(ctx, `SELECT `+logCols+` FROM logs l WHERE l.id=? AND l.game_id=? AND l.review<>'' AND l.draft=0 AND l.deleted_at IS NULL`, id, gameID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydrateLog(ctx, l)
	return l, nil
}

type LogInput struct {
	UserID        int64
	GameID        int64
	Name          string
	Rating        float64
	Review        string
	Spoiler       bool
	Status        string
	Replay        bool
	Mastered      bool
	Difficulty    string
	PlatformID    *int64
	PlayedOnID    *int64
	Ownership     string
	Storefront    string
	EditionGameID *int64
	StartedAt     string
	FinishedAt    string
	TimeToFinish  float64
	TimeToMaster  float64
	Playtime      float64
	SyncSessions  bool
	Language      string
	Draft         bool
}

func (in LogInput) statusOr() string {
	if in.Status == "" {
		return "played"
	}
	return in.Status
}

func (s *Store) CreateLog(ctx context.Context, in LogInput) (*models.Log, error) {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO logs(user_id,game_id,name,rating,review,spoiler,status,replay,mastered,
		difficulty,platform_id,played_on_id,ownership,storefront,edition_game_id,started_at,finished_at,time_to_finish,
		time_to_master,playtime,sync_sessions,primary_rating,language,draft,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?)`,
		in.UserID, in.GameID, in.Name, in.Rating, in.Review, b2i(in.Spoiler), in.statusOr(), b2i(in.Replay),
		b2i(in.Mastered), in.Difficulty, in.PlatformID, in.PlayedOnID, in.Ownership, in.Storefront, in.EditionGameID,
		nullStr(in.StartedAt), nullStr(in.FinishedAt), in.TimeToFinish, in.TimeToMaster, in.Playtime,
		b2i(in.SyncSessions), in.Language, b2i(in.Draft), now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if in.Rating > 0 && !in.Draft {
		s.SetPrimaryRating(ctx, id, in.UserID, in.GameID)
	}
	s.RefreshGameStats(ctx, in.GameID)
	return s.LogByID(ctx, id)
}

func (s *Store) UpdateLog(ctx context.Context, id int64, in LogInput) (*models.Log, error) {
	_, err := s.DB.ExecContext(ctx, `UPDATE logs SET name=?,rating=?,review=?,spoiler=?,status=?,replay=?,mastered=?,
		difficulty=?,platform_id=?,played_on_id=?,ownership=?,storefront=?,edition_game_id=?,started_at=?,finished_at=?,
		time_to_finish=?,time_to_master=?,playtime=?,sync_sessions=?,language=?,draft=?,updated_at=? WHERE id=? AND user_id=?`,
		in.Name, in.Rating, in.Review, b2i(in.Spoiler), in.statusOr(), b2i(in.Replay), b2i(in.Mastered),
		in.Difficulty, in.PlatformID, in.PlayedOnID, in.Ownership, in.Storefront, in.EditionGameID,
		nullStr(in.StartedAt), nullStr(in.FinishedAt), in.TimeToFinish, in.TimeToMaster, in.Playtime,
		b2i(in.SyncSessions), in.Language, b2i(in.Draft), Now(), id, in.UserID)
	if err != nil {
		return nil, err
	}
	if in.Rating > 0 && in.Draft == false {
		s.SetPrimaryRating(ctx, id, in.UserID, in.GameID)
	}
	s.RefreshGameStats(ctx, in.GameID)
	return s.LogByID(ctx, id)
}

// RateGame records a 0–10 rating without creating a review or replay log.
// When no log exists a minimal played log is created. A zero rating clears the
// latest log's rating. Existing reviews, statuses and play counts are kept.
func (s *Store) RateGame(ctx context.Context, userID, gameID int64, rating float64) error {
	if rating < 0 {
		rating = 0
	}
	if rating > 10 {
		rating = 10
	}
	latest, err := scanLog(s.DB.QueryRowContext(ctx, `SELECT `+logCols+` FROM logs l
		WHERE l.user_id=? AND l.game_id=? AND l.deleted_at IS NULL AND l.draft=0
		ORDER BY l.updated_at DESC LIMIT 1`, userID, gameID))
	if err == sql.ErrNoRows {
		if rating == 0 {
			return nil
		}
		_, err = s.CreateLog(ctx, LogInput{UserID: userID, GameID: gameID, Rating: rating, Status: "played"})
		return err
	}
	if err != nil {
		return err
	}
	in := logInputFromLog(latest)
	in.Rating = rating
	if _, err := s.UpdateLog(ctx, latest.ID, in); err != nil {
		return err
	}
	if rating == 0 {
		_, err = s.DB.ExecContext(ctx, `UPDATE logs SET rating=0,primary_rating=0 WHERE id=?`, latest.ID)
	}
	return err
}

// logInputFromLog copies a stored log back into the editable input shape so a
// rating-only update leaves every other field untouched.
func logInputFromLog(l *models.Log) LogInput {
	return LogInput{
		UserID: l.UserID, GameID: l.GameID, Name: l.Name, Rating: l.Rating, Review: l.Review,
		Spoiler: l.Spoiler, Status: l.Status, Replay: l.Replay, Mastered: l.Mastered,
		Difficulty: l.Difficulty, PlatformID: l.PlatformID, PlayedOnID: l.PlayedOnID,
		Ownership: l.Ownership, Storefront: l.Storefront, EditionGameID: l.EditionGameID,
		StartedAt: l.StartedAt, FinishedAt: l.FinishedAt, TimeToFinish: l.TimeToFinish,
		TimeToMaster: l.TimeToMaster, Playtime: l.Playtime, SyncSessions: l.SyncSessions,
		Language: l.Language, Draft: l.Draft,
	}
}

// SetPrimaryRating makes one log the rating that counts for the game. Other
// logs by the user are demoted but their rating is remembered.
func (s *Store) SetPrimaryRating(ctx context.Context, logID, userID, gameID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.ExecContext(ctx, `UPDATE logs SET primary_rating=0 WHERE user_id=? AND game_id=?`, userID, gameID)
	tx.ExecContext(ctx, `UPDATE logs SET primary_rating=1 WHERE id=?`, logID)
	return tx.Commit()
}

func (s *Store) DeleteLog(ctx context.Context, id, userID int64) error {
	var gameID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT game_id FROM logs WHERE id=? AND user_id=?`, id, userID).Scan(&gameID); err != nil {
		return ErrNotFound
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE logs SET deleted_at=? WHERE id=?`, Now(), id); err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

// ModDeleteLog removes any user's log after a moderator review.
func (s *Store) ModDeleteLog(ctx context.Context, id int64) error {
	var gameID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT game_id FROM logs WHERE id=? AND deleted_at IS NULL`, id).Scan(&gameID); err != nil {
		return ErrNotFound
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE logs SET deleted_at=? WHERE id=?`, Now(), id); err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

func (s *Store) RestoreLog(ctx context.Context, id, userID int64) error {
	var gameID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT game_id FROM logs WHERE id=? AND user_id=?`, id, userID).Scan(&gameID); err != nil {
		return ErrNotFound
	}
	s.DB.ExecContext(ctx, `UPDATE logs SET deleted_at=NULL WHERE id=?`, id)
	return s.RefreshGameStats(ctx, gameID)
}

func (s *Store) PurgeLog(ctx context.Context, id, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM logs WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (s *Store) LatestLogFor(ctx context.Context, userID, gameID int64) (*models.Log, error) {
	l, err := scanLog(s.DB.QueryRowContext(ctx, `SELECT `+logCols+` FROM logs l
		WHERE l.user_id=? AND l.game_id=? AND l.deleted_at IS NULL ORDER BY l.updated_at DESC LIMIT 1`, userID, gameID))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.hydrateLog(ctx, l)
	return l, nil
}

// LogsForGame lists public journal entries for a game. viewerID may be 0.
func (s *Store) LogsForGame(ctx context.Context, gameID, viewerID int64, limit, offset int) ([]*models.Log, int, error) {
	var total int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs l JOIN users u ON u.id=l.user_id
		WHERE l.game_id=? AND l.deleted_at IS NULL AND l.draft=0 AND u.private_profile=0`, gameID).Scan(&total)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l JOIN users u ON u.id=l.user_id
		WHERE l.game_id=? AND l.deleted_at IS NULL AND l.draft=0 AND u.private_profile=0
		ORDER BY l.updated_at DESC LIMIT ? OFFSET ?`, gameID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var out []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for _, l := range out {
		s.hydrateLog(ctx, l)
	}
	return out, total, nil
}

// ReviewsForGame lists only reviews (not bare logs).
func (s *Store) ReviewsForGame(ctx context.Context, gameID int64, sortKey string, limit, offset int) ([]*models.Log, int, error) {
	var total int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs l JOIN users u ON u.id=l.user_id
		WHERE l.game_id=? AND l.review<>'' AND l.draft=0 AND l.deleted_at IS NULL AND u.private_profile=0`, gameID).Scan(&total)
	order := "l.updated_at DESC"
	switch sortKey {
	case "rating":
		order = "l.rating DESC, l.updated_at DESC, l.id DESC"
	case "likes":
		order = "(SELECT COUNT(*) FROM log_likes ll WHERE ll.log_id=l.id) DESC, l.updated_at DESC, l.id DESC"
	case "oldest":
		order = "l.created_at ASC, l.id ASC"
	case "new":
		order = "l.updated_at DESC, l.id DESC"
	default:
		order = "l.updated_at DESC, l.id DESC"
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l JOIN users u ON u.id=l.user_id
		WHERE l.game_id=? AND l.review<>'' AND l.draft=0 AND l.deleted_at IS NULL AND u.private_profile=0
		ORDER BY `+order+` LIMIT ? OFFSET ?`, gameID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var out []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for _, l := range out {
		s.hydrateLog(ctx, l)
	}
	return out, total, nil
}

// ReviewsFeed is the global reviews page, filtered by feed tabs.
func (s *Store) ReviewsFeed(ctx context.Context, viewerID int64, tab string, gameID int64, limit, offset int) ([]*models.Log, int, error) {
	conds := []string{"l.review<>''", "l.draft=0", "l.deleted_at IS NULL", "u.private_profile=0", "u.deactivated_at IS NULL"}
	args := []any{}
	if gameID > 0 {
		conds = append(conds, "l.game_id=?")
		args = append(args, gameID)
	}
	if viewerID > 0 && tab == "friends" {
		friends := s.FriendIDs(ctx, viewerID)
		if len(friends) == 0 {
			return nil, 0, nil
		}
		conds = append(conds, "l.user_id IN ("+placeholders(len(friends))+")")
		for _, f := range friends {
			args = append(args, f)
		}
	}
	where := " WHERE " + strings.Join(conds, " AND ")
	var total int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs l JOIN users u ON u.id=l.user_id`+where, args...).Scan(&total)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l JOIN users u ON u.id=l.user_id`+where+`
		ORDER BY l.updated_at DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	var out []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for _, l := range out {
		s.hydrateLog(ctx, l)
	}
	return out, total, nil
}

// SearchReviews searches published review titles and bodies, plus the game
// title so a game-name query finds reviews of that game.
func (s *Store) SearchReviews(ctx context.Context, q string, limit, offset int, includeAdult bool) ([]*models.Log, int, error) {
	if strings.TrimSpace(q) == "" {
		return nil, 0, nil
	}
	conds := []string{
		"l.review<>''", "l.draft=0", "l.deleted_at IS NULL",
		"u.private_profile=0", "u.deactivated_at IS NULL",
	}
	args := []any{}
	match := ftsQuery(q)
	title := "%" + escapeLike(q) + "%"
	if match != "" {
		conds = append(conds, `(l.id IN (SELECT rowid FROM logs_fts WHERE logs_fts MATCH ?) OR g.title LIKE ? ESCAPE '\')`)
		args = append(args, match, title)
	} else {
		conds = append(conds, `g.title LIKE ? ESCAPE '\'`)
		args = append(args, title)
	}
	if !includeAdult {
		conds = append(conds, "g.adult=0")
	}
	where := " WHERE " + strings.Join(conds, " AND ")
	from := ` FROM logs l JOIN users u ON u.id=l.user_id JOIN games g ON g.id=l.game_id`
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*)`+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+from+where+`
		ORDER BY l.updated_at DESC, l.id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	var out []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for _, l := range out {
		s.hydrateLog(ctx, l)
	}
	return out, total, nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// UserLogs returns a user's journal respecting profile privacy for viewers.
func (s *Store) UserLogs(ctx context.Context, userID int64, hasReview bool, limit, offset int) ([]*models.Log, int, error) {
	extra := ""
	if hasReview {
		extra = " AND l.review<>''"
	}
	var total int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs l WHERE l.user_id=? AND l.deleted_at IS NULL`+extra, userID).Scan(&total)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l WHERE l.user_id=? AND l.deleted_at IS NULL`+extra+`
		ORDER BY l.updated_at DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var out []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	for _, l := range out {
		s.hydrateLog(ctx, l)
	}
	return out, total, nil
}

func (s *Store) DraftsFor(ctx context.Context, userID int64) ([]*models.Log, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l WHERE l.user_id=? AND l.draft=1 AND l.deleted_at IS NULL ORDER BY l.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	var out []*models.Log
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
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

func (s *Store) LikeLog(ctx context.Context, logID, userID int64, like bool) error {
	var err error
	if like {
		_, err = s.DB.ExecContext(ctx, `INSERT INTO log_likes(log_id,user_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, logID, userID, Now())
	} else {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM log_likes WHERE log_id=? AND user_id=?`, logID, userID)
	}
	return err
}

func (s *Store) HasLikedLog(ctx context.Context, logID, userID int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM log_likes WHERE log_id=? AND user_id=?`, logID, userID).Scan(&n)
	return n > 0
}

// ---- statuses ----

func (s *Store) StatusFor(ctx context.Context, userID, gameID int64) (models.Status, error) {
	st := models.Status{UserID: userID, GameID: gameID}
	var playing, backlog, wishlist, liked int
	var updated string
	err := s.DB.QueryRowContext(ctx, `SELECT played,playing,backlog,wishlist,updated_at FROM statuses WHERE user_id=? AND game_id=?`, userID, gameID).
		Scan(&st.Played, &playing, &backlog, &wishlist, &updated)
	if err != nil && err != sql.ErrNoRows {
		return st, err
	}
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_likes WHERE user_id=? AND game_id=?`, userID, gameID).Scan(&liked)
	st.Playing = playing == 1
	st.Backlog = backlog == 1
	st.Wishlist = wishlist == 1
	st.Liked = liked > 0
	if err == nil {
		st.UpdatedAt = parseTime(updated)
	}
	return st, nil
}

// SetStatusFlag turns one shelf flag on or off and keeps the others.
func (s *Store) SetStatusFlag(ctx context.Context, userID, gameID int64, flag string, on bool) error {
	switch flag {
	case "playing", "backlog", "wishlist":
	default:
		return ErrNotFound
	}
	now := Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO statuses(user_id,game_id,`+flag+`,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id,game_id) DO UPDATE SET `+flag+`=?,updated_at=?`, userID, gameID, b2i(on), now, b2i(on), now)
	if err != nil {
		return err
	}
	if err := s.cleanupEmptyStatus(ctx, userID, gameID); err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

// SetPlayedCount sets the user's play counter exactly, clamping it at zero.
func (s *Store) SetPlayedCount(ctx context.Context, userID, gameID int64, count int) error {
	if count < 0 {
		count = 0
	}
	now := Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO statuses(user_id,game_id,played,updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(user_id,game_id) DO UPDATE SET played=?,updated_at=?`,
		userID, gameID, count, now, count, now)
	if err != nil {
		return err
	}
	if err := s.cleanupEmptyStatus(ctx, userID, gameID); err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

// EnsurePlayed raises the play counter to at least one.
func (s *Store) EnsurePlayed(ctx context.Context, userID, gameID int64) error {
	now := Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO statuses(user_id,game_id,played,updated_at)
		VALUES(?,?,1,?)
		ON CONFLICT(user_id,game_id) DO UPDATE SET played=MAX(played,1),updated_at=?`,
		userID, gameID, now, now)
	if err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

func (s *Store) cleanupEmptyStatus(ctx context.Context, userID, gameID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM statuses
		WHERE user_id=? AND game_id=? AND played=0 AND playing=0 AND backlog=0 AND wishlist=0`, userID, gameID)
	return err
}

// ToggleGameLike flips a game like and reports the new state. Likes stay
// separate from shelf flags and play counts.
func (s *Store) ToggleGameLike(ctx context.Context, userID, gameID int64) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_likes WHERE user_id=? AND game_id=?`, userID, gameID).Scan(&existing); err != nil {
		return false, err
	}
	liked := existing == 0
	if liked {
		_, err = tx.ExecContext(ctx, `INSERT INTO game_likes(user_id,game_id,created_at) VALUES(?,?,?)`, userID, gameID, Now())
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM game_likes WHERE user_id=? AND game_id=?`, userID, gameID)
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if err := s.RefreshGameStats(ctx, gameID); err != nil {
		return false, err
	}
	return liked, nil
}

// SetStatuses replaces all shelf flags in one write. Imports use this so a
// single game refresh covers every flag instead of one per shelf. A played
// import keeps any higher play count the user has already recorded.
func (s *Store) SetStatuses(ctx context.Context, userID, gameID int64, played, playing, backlog, wishlist, liked bool) error {
	now := Now()
	playedCount := 0
	if played {
		playedCount = 1
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO statuses(user_id,game_id,played,playing,backlog,wishlist,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user_id,game_id) DO UPDATE SET
		played=CASE WHEN excluded.played>0 THEN MAX(statuses.played,1) ELSE statuses.played END,
		playing=excluded.playing,backlog=excluded.backlog,wishlist=excluded.wishlist,updated_at=excluded.updated_at`,
		userID, gameID, playedCount, b2i(playing), b2i(backlog), b2i(wishlist), now)
	if err != nil {
		return err
	}
	if err := s.cleanupEmptyStatus(ctx, userID, gameID); err != nil {
		return err
	}
	if liked {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO game_likes(user_id,game_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, userID, gameID, now); err != nil {
			return err
		}
	} else if _, err := s.DB.ExecContext(ctx, `DELETE FROM game_likes WHERE user_id=? AND game_id=?`, userID, gameID); err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

func (s *Store) ClearStatus(ctx context.Context, userID, gameID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM statuses WHERE user_id=? AND game_id=?`, userID, gameID)
	if err != nil {
		return err
	}
	return s.RefreshGameStats(ctx, gameID)
}

// GameStatusCounts returns counts per shelf and the viewer's status.
func (s *Store) GameStatusCounts(ctx context.Context, gameID int64) map[string]int {
	out := map[string]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN played>0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(playing),0),COALESCE(SUM(backlog),0),COALESCE(SUM(wishlist),0)
		FROM statuses WHERE game_id=?`, gameID)
	if err != nil {
		return out
	}
	var p, pl, b, w int
	if rows.Next() {
		rows.Scan(&p, &pl, &b, &w)
	}
	rows.Close()
	out["played"] = p
	out["playing"] = pl
	out["backlog"] = b
	out["wishlist"] = w
	var likes int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_likes WHERE game_id=?`, gameID).Scan(&likes)
	out["liked"] = likes
	return out
}

// RatingDistribution powers the histogram on game pages.
func (s *Store) RatingDistribution(ctx context.Context, gameID int64, scale int) map[int]int {
	out := map[int]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT rating,COUNT(*) FROM logs WHERE game_id=? AND rating>0 AND primary_rating=1 AND deleted_at IS NULL GROUP BY rating`, gameID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r float64
		var n int
		rows.Scan(&r, &n)
		bucket := int(r)
		if scale == 5 {
			bucket = int(r / 2)
			if bucket < 1 {
				bucket = 1
			}
		}
		out[bucket] += n
	}
	return out
}

// RatingForUser returns the rating that counts, if the user has one.
func (s *Store) RatingForUser(ctx context.Context, userID, gameID int64) float64 {
	var r float64
	s.DB.QueryRowContext(ctx, `SELECT rating FROM logs WHERE user_id=? AND game_id=? AND primary_rating=1 AND deleted_at IS NULL AND rating>0 ORDER BY updated_at DESC LIMIT 1`, userID, gameID).Scan(&r)
	return r
}

// ---- sessions ----

func (s *Store) SessionsFor(ctx context.Context, logID int64) ([]models.Session, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,note,note_privacy,playtime,COALESCE(started_on,''),COALESCE(ended_on,''),created_at
		FROM play_sessions WHERE log_id=? ORDER BY COALESCE(started_on,created_at) DESC`, logID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Session
	for rows.Next() {
		var x models.Session
		var created string
		x.LogID = logID
		rows.Scan(&x.ID, &x.Note, &x.NotePrivacy, &x.Playtime, &x.StartedOn, &x.EndedOn, &created)
		x.CreatedAt = parseTime(created)
		x.Days, _ = s.sessionStrings(ctx, "play_session_days", "day", x.ID)
		x.Tags, _ = s.sessionStrings(ctx, "play_session_tags", "tag", x.ID)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) sessionStrings(ctx context.Context, table, col string, id int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+col+` FROM `+table+` WHERE session_id=? ORDER BY 1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		rows.Scan(&v)
		out = append(out, v)
	}
	return out, rows.Err()
}

type SessionInput struct {
	LogID       int64
	Note        string
	NotePrivacy string
	Playtime    float64
	Days        []string
	Tags        []string
}

func (s *Store) CreateSession(ctx context.Context, in SessionInput) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if in.NotePrivacy == "" {
		in.NotePrivacy = "friends"
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO play_sessions(log_id,note,note_privacy,playtime,created_at) VALUES(?,?,?,?,?)`,
		in.LogID, in.Note, in.NotePrivacy, in.Playtime, Now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	for _, d := range in.Days {
		if d != "" {
			tx.ExecContext(ctx, `INSERT OR IGNORE INTO play_session_days(session_id,day) VALUES(?,?)`, id, d)
		}
	}
	for _, t := range in.Tags {
		if t != "" {
			tx.ExecContext(ctx, `INSERT OR IGNORE INTO play_session_tags(session_id,tag) VALUES(?,?)`, id, strings.ToLower(t))
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.recalcPlaytime(ctx, in.LogID)
	return id, nil
}

func (s *Store) UpdateSession(ctx context.Context, id int64, in SessionInput) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if in.NotePrivacy == "" {
		in.NotePrivacy = "friends"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE play_sessions SET note=?,note_privacy=?,playtime=? WHERE id=?`, in.Note, in.NotePrivacy, in.Playtime, id); err != nil {
		return err
	}
	tx.ExecContext(ctx, `DELETE FROM play_session_days WHERE session_id=?`, id)
	tx.ExecContext(ctx, `DELETE FROM play_session_tags WHERE session_id=?`, id)
	for _, d := range in.Days {
		if d != "" {
			tx.ExecContext(ctx, `INSERT OR IGNORE INTO play_session_days(session_id,day) VALUES(?,?)`, id, d)
		}
	}
	for _, t := range in.Tags {
		if t != "" {
			tx.ExecContext(ctx, `INSERT OR IGNORE INTO play_session_tags(session_id,tag) VALUES(?,?)`, id, strings.ToLower(t))
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.recalcPlaytime(ctx, in.LogID)
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, id, userID int64) error {
	var logID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT ps.log_id FROM play_sessions ps JOIN logs l ON l.id=ps.log_id WHERE ps.id=? AND l.user_id=?`, id, userID).Scan(&logID); err != nil {
		return ErrNotFound
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM play_sessions WHERE id=?`, id); err != nil {
		return err
	}
	s.recalcPlaytime(ctx, logID)
	return nil
}

func (s *Store) recalcPlaytime(ctx context.Context, logID int64) {
	var total float64
	s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(playtime),0) FROM play_sessions WHERE log_id=?`, logID).Scan(&total)
	if total > 0 {
		s.DB.ExecContext(ctx, `UPDATE logs SET playtime=? WHERE id=? AND sync_sessions=1`, total, logID)
	}
}

// PlayDays returns the set of days the user played a game, for the calendar.
func (s *Store) PlayDays(ctx context.Context, userID, gameID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT psd.day FROM play_session_days psd
		JOIN play_sessions ps ON ps.id=psd.session_id JOIN logs l ON l.id=ps.log_id
		WHERE l.user_id=? AND l.game_id=? ORDER BY psd.day`, userID, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		rows.Scan(&d)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- library ----

func (s *Store) LibraryFor(ctx context.Context, userID int64) ([]models.LibraryEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,user_id,game_id,platform_id,ownership,storefront,edition,created_at
		FROM library_entries WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.LibraryEntry
	for rows.Next() {
		var e models.LibraryEntry
		var platform sql.NullInt64
		var created string
		rows.Scan(&e.ID, &e.UserID, &e.GameID, &platform, &e.Ownership, &e.Storefront, &e.Edition, &created)
		if platform.Valid {
			e.PlatformID = &platform.Int64
			var name string
			s.DB.QueryRowContext(ctx, `SELECT name FROM platforms WHERE id=?`, platform.Int64).Scan(&name)
			e.Platform = name
		}
		e.CreatedAt = parseTime(created)
		if g, err := s.GameByID(ctx, e.GameID); err == nil {
			e.Game = g
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) LibraryEntry(ctx context.Context, userID, gameID int64) (*models.LibraryEntry, error) {
	var e models.LibraryEntry
	var platform sql.NullInt64
	var created string
	err := s.DB.QueryRowContext(ctx, `SELECT id,user_id,game_id,platform_id,ownership,storefront,edition,created_at
		FROM library_entries WHERE user_id=? AND game_id=?`, userID, gameID).
		Scan(&e.ID, &e.UserID, &e.GameID, &platform, &e.Ownership, &e.Storefront, &e.Edition, &created)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if platform.Valid {
		e.PlatformID = &platform.Int64
	}
	e.CreatedAt = parseTime(created)
	return &e, nil
}

func (s *Store) SaveLibraryEntry(ctx context.Context, userID, gameID int64, platformID *int64, ownership, storefront, edition string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO library_entries(user_id,game_id,platform_id,ownership,storefront,edition,created_at)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, userID, gameID, platformID, ownership, storefront, edition, Now())
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE library_entries SET platform_id=?,ownership=?,storefront=?,edition=? WHERE user_id=? AND game_id=?`,
		platformID, ownership, storefront, edition, userID, gameID)
	return err
}

func (s *Store) RemoveLibraryEntry(ctx context.Context, userID, gameID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM library_entries WHERE user_id=? AND game_id=?`, userID, gameID)
	return err
}

// ---- covers / hidden games ----

func (s *Store) SetCover(ctx context.Context, userID, gameID int64, url string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO covers(user_id,game_id,url) VALUES(?,?,?)
		ON CONFLICT(user_id,game_id) DO UPDATE SET url=excluded.url`, userID, gameID, url)
	return err
}

func (s *Store) CoverFor(ctx context.Context, userID, gameID int64) string {
	var url string
	s.DB.QueryRowContext(ctx, `SELECT url FROM covers WHERE user_id=? AND game_id=?`, userID, gameID).Scan(&url)
	return url
}

// PlayerCovers shows which cover each user picked on the game page.
func (s *Store) PlayerCovers(ctx context.Context, gameID int64) map[int64]string {
	out := map[int64]string{}
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id,url FROM covers WHERE game_id=?`, gameID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var uid int64
		var url string
		rows.Scan(&uid, &url)
		out[uid] = url
	}
	return out
}

func (s *Store) HideGame(ctx context.Context, userID, gameID int64, hide bool) error {
	if hide {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO hidden_games(user_id,game_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, userID, gameID, Now())
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM hidden_games WHERE user_id=? AND game_id=?`, userID, gameID)
	return err
}

func (s *Store) IsGameHidden(ctx context.Context, userID, gameID int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM hidden_games WHERE user_id=? AND game_id=?`, userID, gameID).Scan(&n)
	return n > 0
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// CountLogLikes returns the number of likes on a log.
func (s *Store) CountLogLikes(ctx context.Context, logID int64) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM log_likes WHERE log_id=?`, logID).Scan(&n)
	return n
}

// GameLikes returns recent users who liked a game and the total count.
func (s *Store) GameLikes(ctx context.Context, gameID int64, limit int) (int, []*models.User) {
	var total int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_likes WHERE game_id=?`, gameID).Scan(&total)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+prefixedUserCols("u")+` FROM game_likes gl JOIN users u ON u.id=gl.user_id
		WHERE gl.game_id=? ORDER BY gl.created_at DESC LIMIT ?`, gameID, limit)
	if err != nil {
		return total, nil
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return total, out
		}
		out = append(out, u)
	}
	return total, out
}
