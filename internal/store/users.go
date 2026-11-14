package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"luduslog/internal/models"
)

const userCols = `id,username,email,email_verified,approved,password_hash,bio,avatar,header,role,created_at,
deactivated_at,last_username_change,private_profile,hide_activity,manually_approves,hide_social_graph,adult_optin,
star_scale,cover_mode,time_display,infinite_scroll,theme,high_contrast,timezone,fade_played,journal_view,
comment_privacy,note_privacy,federation,fed_statuses,fed_reviews,fed_lists,remote_actor,social_links`

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (*models.User, error) {
	var u models.User
	var createdAt, social string
	var deact, lastChange sql.NullString
	var emailVerified, approved, private, hideAct, manual, hideGraph, adult, scroll, hc, fade, fed, fedS, fedR, fedL int
	err := row.Scan(&u.ID, &u.Username, &u.Email, &emailVerified, &approved, &u.PasswordHash, &u.Bio, &u.Avatar, &u.Header,
		&u.Role, &createdAt, &deact, &lastChange, &private, &hideAct, &manual, &hideGraph, &adult,
		&u.StarScale, &u.CoverMode, &u.TimeDisplay, &scroll, &u.Theme, &hc, &u.Timezone, &fade, &u.JournalView,
		&u.CommentPrivacy, &u.NotePrivacy, &fed, &fedS, &fedR, &fedL, &u.RemoteActor, &social)
	if err != nil {
		return nil, err
	}
	u.EmailVerified = emailVerified == 1
	u.Approved = approved == 1
	if u.Role == "" {
		u.Role = "user"
	}
	u.PrivateProfile = private == 1
	u.HideActivity = hideAct == 1
	u.ManuallyApproves = manual == 1
	u.HideSocialGraph = hideGraph == 1
	u.AdultOptIn = adult == 1
	u.InfiniteScroll = scroll == 1
	u.HighContrast = hc == 1
	u.FadePlayed = fade == 1
	u.Federation = fed == 1
	u.FedStatuses = fedS == 1
	u.FedReviews = fedR == 1
	u.FedLists = fedL == 1
	u.CreatedAt = parseTime(createdAt)
	u.DeactivatedAt = parseNullTime(deact)
	u.LastUsernameChange = parseNullTime(lastChange)
	if u.StarScale != 10 {
		u.StarScale = 5
	}
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, username, email, passwordHash string, admin bool) (*models.User, error) {
	now := Now()
	role := "user"
	if admin {
		role = "admin"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO users(username,username_lower,email,email_lower,password_hash,created_at,role,actor_key_pem)
		VALUES(?,?,?,?,?,?,?,'')`, username, strings.ToLower(username), email, strings.ToLower(email), passwordHash, now, role)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id int64) (*models.User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) UserByUsername(ctx context.Context, name string) (*models.User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username_lower=?`, strings.ToLower(name)))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*models.User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email_lower=?`, strings.ToLower(email)))
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) UserExists(ctx context.Context, username, email string) (bool, bool) {
	var un, em int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username_lower=?`, strings.ToLower(username)).Scan(&un)
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email_lower=?`, strings.ToLower(email)).Scan(&em)
	return un > 0, em > 0
}

func (s *Store) CountUsers(ctx context.Context) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

func (s *Store) SetUserPassword(ctx context.Context, id int64, hash string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, hash, id)
	return err
}

func (s *Store) SetEmailVerified(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET email_verified=1 WHERE id=?`, id)
	return err
}

func (s *Store) UpdateEmail(ctx context.Context, id int64, email string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET email=?,email_lower=?,email_verified=0 WHERE id=?`, email, strings.ToLower(email), id)
	return err
}

// ProfileUpdate carries the editable profile/settings surface.
type ProfileUpdate struct {
	Bio              string
	SocialLinks      []models.SocialLink
	Avatar           string
	Header           string
	PrivateProfile   bool
	HideActivity     bool
	ManuallyApproves bool
	HideSocialGraph  bool
	AdultOptIn       bool
	StarScale        int
	CoverMode        string
	TimeDisplay      string
	InfiniteScroll   bool
	Theme            string
	HighContrast     bool
	Timezone         string
	FadePlayed       bool
	JournalView      string
	CommentPrivacy   string
	NotePrivacy      string
	Federation       bool
	FedStatuses      bool
	FedReviews       bool
	FedLists         bool
}

func (s *Store) UpdateProfile(ctx context.Context, id int64, p ProfileUpdate) error {
	links, _ := json.Marshal(p.SocialLinks)
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET bio=?,social_links=?,avatar=?,header=?,private_profile=?,hide_activity=?,
		manually_approves=?,hide_social_graph=?,adult_optin=?,star_scale=?,cover_mode=?,time_display=?,infinite_scroll=?,
		theme=?,high_contrast=?,timezone=?,fade_played=?,journal_view=?,comment_privacy=?,note_privacy=?,
		federation=?,fed_statuses=?,fed_reviews=?,fed_lists=? WHERE id=?`,
		p.Bio, string(links), p.Avatar, p.Header, b2i(p.PrivateProfile), b2i(p.HideActivity), b2i(p.ManuallyApproves),
		b2i(p.HideSocialGraph), b2i(p.AdultOptIn), p.StarScale, p.CoverMode, p.TimeDisplay, b2i(p.InfiniteScroll),
		p.Theme, b2i(p.HighContrast), p.Timezone, b2i(p.FadePlayed), p.JournalView, p.CommentPrivacy, p.NotePrivacy,
		b2i(p.Federation), b2i(p.FedStatuses), b2i(p.FedReviews), b2i(p.FedLists), id)
	return err
}

func (s *Store) SocialLinks(u *models.User) []models.SocialLink {
	var out []models.SocialLink
	// Social links are stored as JSON in the users row; the web layer loads
	// them through ProfileData to avoid a second query.
	_ = json.Unmarshal([]byte("[]"), &out)
	return out
}

func (s *Store) SocialLinksFor(ctx context.Context, id int64) []models.SocialLink {
	var raw string
	if err := s.DB.QueryRowContext(ctx, `SELECT social_links FROM users WHERE id=?`, id).Scan(&raw); err != nil {
		return nil
	}
	var out []models.SocialLink
	json.Unmarshal([]byte(raw), &out)
	return out
}

func (s *Store) ChangeUsername(ctx context.Context, id int64, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET username=?,username_lower=?,last_username_change=? WHERE id=?`,
		name, strings.ToLower(name), Now(), id)
	return err
}

func (s *Store) SetDeactivated(ctx context.Context, id int64, at *string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET deactivated_at=? WHERE id=?`, at, id)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

// SearchUsers powers @mention completion and user search.
func (s *Store) SearchUsers(ctx context.Context, q string, limit int) ([]*models.User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users
		WHERE deactivated_at IS NULL AND (username_lower LIKE ? OR bio LIKE ?) ORDER BY username LIMIT ?`,
		"%"+strings.ToLower(q)+"%", "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ---- follows / blocks ----

func (s *Store) Follow(ctx context.Context, follower, followee int64) error {
	approved := 1
	var manual int
	s.DB.QueryRowContext(ctx, `SELECT manually_approves FROM users WHERE id=?`, followee).Scan(&manual)
	if manual == 1 {
		approved = 0
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO follows(follower_id,followee_id,approved,created_at) VALUES(?,?,?,?)
		ON CONFLICT(follower_id,followee_id) DO NOTHING`, follower, followee, approved, Now())
	return err
}

func (s *Store) ApproveFollow(ctx context.Context, follower, followee int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE follows SET approved=1 WHERE follower_id=? AND followee_id=?`, follower, followee)
	return err
}

func (s *Store) Unfollow(ctx context.Context, follower, followee int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM follows WHERE follower_id=? AND followee_id=?`, follower, followee)
	return err
}

func (s *Store) IsFollowing(ctx context.Context, follower, followee int64) (bool, bool) {
	var approved int
	err := s.DB.QueryRowContext(ctx, `SELECT approved FROM follows WHERE follower_id=? AND followee_id=?`, follower, followee).Scan(&approved)
	return err == nil, approved == 1
}

func (s *Store) FollowsYou(ctx context.Context, a, b int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows WHERE follower_id=? AND followee_id=? AND approved=1`, b, a).Scan(&n)
	return n > 0
}

func (s *Store) Mutual(ctx context.Context, a, b int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows f1 JOIN follows f2
		ON f1.follower_id=f2.followee_id AND f1.followee_id=f2.follower_id
		WHERE f1.follower_id=? AND f1.followee_id=? AND f1.approved=1 AND f2.approved=1`, a, b).Scan(&n)
	return n > 0
}

func (s *Store) CountFollowers(ctx context.Context, id int64) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows WHERE followee_id=? AND approved=1`, id).Scan(&n)
	return n
}

func (s *Store) CountFollowing(ctx context.Context, id int64) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows WHERE follower_id=? AND approved=1`, id).Scan(&n)
	return n
}

func (s *Store) Followers(ctx context.Context, id int64, limit, offset int) ([]*models.User, error) {
	return s.followUsers(ctx, `SELECT `+prefixedUserCols("u")+` FROM follows f JOIN users u ON u.id=f.follower_id
		WHERE f.followee_id=? AND f.approved=1 ORDER BY f.created_at DESC LIMIT ? OFFSET ?`, id, limit, offset)
}

func (s *Store) Following(ctx context.Context, id int64, limit, offset int) ([]*models.User, error) {
	return s.followUsers(ctx, `SELECT `+prefixedUserCols("u")+` FROM follows f JOIN users u ON u.id=f.followee_id
		WHERE f.follower_id=? AND f.approved=1 ORDER BY f.created_at DESC LIMIT ? OFFSET ?`, id, limit, offset)
}

func prefixedUserCols(p string) string {
	parts := strings.Split(userCols, ",")
	for i := range parts {
		parts[i] = p + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ",")
}

func (s *Store) followUsers(ctx context.Context, query string, args ...any) ([]*models.User, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// FriendIDs returns approved mutual follows, used by friends-only filters.
func (s *Store) FriendIDs(ctx context.Context, id int64) []int64 {
	rows, err := s.DB.QueryContext(ctx, `SELECT f1.followee_id FROM follows f1 JOIN follows f2
		ON f1.follower_id=f2.followee_id AND f1.followee_id=f2.follower_id
		WHERE f1.follower_id=? AND f1.approved=1 AND f2.approved=1`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var x int64
		rows.Scan(&x)
		ids = append(ids, x)
	}
	return ids
}

func (s *Store) IsBlocked(ctx context.Context, a, b int64) bool {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks WHERE (blocker_id=? AND blocked_id=?) OR (blocker_id=? AND blocked_id=?)`,
		a, b, b, a).Scan(&n)
	return n > 0
}

func (s *Store) Block(ctx context.Context, blocker, blocked int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	tx.ExecContext(ctx, `INSERT INTO blocks(blocker_id,blocked_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, blocker, blocked, now)
	tx.ExecContext(ctx, `DELETE FROM follows WHERE (follower_id=? AND followee_id=?) OR (follower_id=? AND followee_id=?)`, blocker, blocked, blocked, blocker)
	tx.ExecContext(ctx, `DELETE FROM notifications WHERE user_id=? AND actor_id=?`, blocker, blocked)
	return tx.Commit()
}

func (s *Store) Unblock(ctx context.Context, blocker, blocked int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM blocks WHERE blocker_id=? AND blocked_id=?`, blocker, blocked)
	return err
}

func (s *Store) BlockedUsers(ctx context.Context, id int64) ([]*models.User, error) {
	return s.followUsers(ctx, `SELECT `+prefixedUserCols("u")+` FROM blocks b JOIN users u ON u.id=b.blocked_id
		WHERE b.blocker_id=? ORDER BY b.created_at DESC`, id)
}

// FriendsWhoInteracted returns up to n friends with a status on a game.
func (s *Store) FriendsWhoInteracted(ctx context.Context, me, gameID int64, n int) []struct {
	User   *models.User
	Rating float64
	Status models.Status
} {
	friends := s.FriendIDs(ctx, me)
	var out []struct {
		User   *models.User
		Rating float64
		Status models.Status
	}
	for _, fid := range friends {
		if len(out) >= n {
			break
		}
		u, err := s.UserByID(ctx, fid)
		if err != nil {
			continue
		}
		st, _ := s.StatusFor(ctx, fid, gameID)
		var rating float64
		s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(rating),0) FROM logs WHERE user_id=? AND game_id=? AND deleted_at IS NULL`, fid, gameID).Scan(&rating)
		if st.Played || st.Playing || st.Backlog || st.Wishlist || rating > 0 {
			out = append(out, struct {
				User   *models.User
				Rating float64
				Status models.Status
			}{u, rating, st})
		}
	}
	return out
}

// UsernameChangedRecently enforces the 30-day username change rule.
func (s *Store) UsernameChangedRecently(u *models.User) bool {
	if u.LastUsernameChange == nil {
		return false
	}
	return time.Since(*u.LastUsernameChange) < 30*24*time.Hour
}

// SetApproved toggles the admin approval flag for registration mode "approval".
func (s *Store) SetApproved(ctx context.Context, id int64, approved bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET approved=? WHERE id=?`, b2i(approved), id)
	return err
}

// SetRole changes a user's role. Callers must authorize the change.
func (s *Store) SetRole(ctx context.Context, id int64, role string) error {
	if role != "user" && role != "mod" && role != "admin" {
		return fmt.Errorf("invalid role %q", role)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, role, id)
	return err
}

func (s *Store) AdminCount(ctx context.Context) int {
	var n int
	s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND deactivated_at IS NULL`).Scan(&n)
	return n
}

// AllUsers lists accounts newest first for the admin dashboard.
func (s *Store) AllUsers(ctx context.Context, q string, limit, offset int) ([]*models.User, int, error) {
	where := "WHERE 1=1"
	args := []any{}
	if q != "" {
		where += " AND (username LIKE ? OR email LIKE ?)"
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

// LogModAction records a moderation action using the columns the schema
// actually has. targetUser and reportID are optional.
func (s *Store) LogModAction(ctx context.Context, moderator int64, action string, targetUser *int64, reason string, reportID *int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mod_actions(moderator,target_user,action,reason,report_id,created_at)
		VALUES(?,?,?,?,?,?)`, moderator, targetUser, action, reason, reportID, Now())
	return err
}

func (s *Store) ModActions(ctx context.Context, limit int) ([]models.ModAction, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id,m.moderator,COALESCE(u.username,''),m.target_user,
		COALESCE(t.username,''),m.action,m.reason,m.report_id,m.created_at
		FROM mod_actions m LEFT JOIN users u ON u.id=m.moderator LEFT JOIN users t ON t.id=m.target_user
		ORDER BY m.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ModAction
	for rows.Next() {
		var a models.ModAction
		var modID, targetID, reportID sql.NullInt64
		var created string
		if err := rows.Scan(&a.ID, &modID, &a.Moderator, &targetID, &a.TargetName, &a.Action, &a.Reason, &reportID, &created); err != nil {
			return nil, err
		}
		if modID.Valid {
			a.ModeratorID = &modID.Int64
		}
		if targetID.Valid {
			a.TargetUser = &targetID.Int64
		}
		if reportID.Valid {
			a.ReportID = &reportID.Int64
		}
		a.CreatedAt = parseTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Audit(ctx context.Context, actorID int64, action, detail string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO admin_audit(actor_id,action,detail,created_at) VALUES(?,?,?,?)`,
		actorID, action, detail, Now())
	return err
}

type AuditEntry struct {
	ID        int64
	Actor     string
	Action    string
	Detail    string
	CreatedAt time.Time
}

func (s *Store) AuditEntries(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id,COALESCE(u.username,'system'),a.action,a.detail,a.created_at
		FROM admin_audit a LEFT JOIN users u ON u.id=a.actor_id ORDER BY a.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var created string
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Detail, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MutualUsers returns approved mutual follows (friends).
func (s *Store) MutualUsers(ctx context.Context, id int64, limit int) ([]*models.User, error) {
	return s.followUsers(ctx, `SELECT `+prefixedUserCols("u")+` FROM follows f1
		JOIN follows f2 ON f1.follower_id=f2.followee_id AND f1.followee_id=f2.follower_id
		JOIN users u ON u.id=f1.followee_id
		WHERE f1.follower_id=? AND f1.approved=1 AND f2.approved=1
		ORDER BY u.username LIMIT ?`, id, limit)
}
