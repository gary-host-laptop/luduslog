package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"luduslog/internal/models"
)

// ActivityKinds are the privacy buckets for the unified timeline.
var ActivityKinds = []string{"review", "log", "session", "list", "rating", "like", "shelf", "follow"}

var DefaultActivityVisibility = map[string]string{
	"review":  "everyone",
	"log":     "everyone",
	"session": "everyone",
	"list":    "everyone",
	"rating":  "everyone",
	"like":    "everyone",
	"shelf":   "everyone",
	"follow":  "followers",
}

// ActivityVisibility returns every kind, filling unset rows with its default.
func (s *Store) ActivityVisibility(ctx context.Context, userID int64) (map[string]string, error) {
	out := make(map[string]string, len(ActivityKinds))
	for _, kind := range ActivityKinds {
		out[kind] = DefaultActivityVisibility[kind]
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT kind,visibility FROM activity_visibility WHERE user_id=?`, userID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, visibility string
		if err := rows.Scan(&kind, &visibility); err != nil {
			return out, err
		}
		if _, ok := out[kind]; ok && validActivityVisibility(visibility) {
			out[kind] = visibility
		}
	}
	return out, rows.Err()
}

func validActivityVisibility(v string) bool {
	return v == "everyone" || v == "followers" || v == "private"
}

// SetActivityVisibility stores non-default choices and removes rows that revert
// to a default, so an absent row always means the documented default.
func (s *Store) SetActivityVisibility(ctx context.Context, userID int64, values map[string]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM activity_visibility WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, kind := range ActivityKinds {
		visibility := strings.ToLower(strings.TrimSpace(values[kind]))
		if !validActivityVisibility(visibility) {
			visibility = DefaultActivityVisibility[kind]
		}
		if visibility == DefaultActivityVisibility[kind] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO activity_visibility(user_id,kind,visibility) VALUES(?,?,?)`, userID, kind, visibility); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func timelineKey(kind string, id int64) string {
	return fmt.Sprintf("%s:%020d", kind, id)
}

func timelineCursorTime(raw string) (time.Time, string, bool) {
	at, key, ok := strings.Cut(raw, "|")
	if !ok || key == "" {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", false
	}
	return t, key, true
}

func timelineCursorID(key string) string {
	if i := strings.LastIndex(key, ":"); i >= 0 {
		return key[i+1:]
	}
	return ""
}

func timelineKindForActivityType(t string) string {
	switch t {
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
	default:
		return ""
	}
}

func timelineKindWanted(filter, kind string) bool {
	switch filter {
	case "", "all":
		return true
	case "reviews":
		return kind == "review"
	case "logs":
		return kind == "log"
	case "sessions":
		return kind == "session"
	case "shelves":
		return kind == "shelf"
	case "lists":
		return kind == "list"
	case "social":
		return kind == "like" || kind == "rating" || kind == "follow"
	default:
		return true
	}
}

// Timeline returns the unified stream for one or more owners. Content is read
// from logs and play_sessions; social rows come from activities. Pagination is
// keyset-based on (created_at, stable key) and merges each source in Go.
func (s *Store) Timeline(ctx context.Context, viewerID int64, ownerIDs []int64, filter, cursor string, limit int) ([]models.TimelineItem, string, error) {
	ownerIDs = uniqueIDs(ownerIDs)
	if len(ownerIDs) == 0 {
		return nil, "", nil
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	var (
		before    time.Time
		beforeKey string
		hasCursor bool
	)
	if cursor != "" {
		before, beforeKey, hasCursor = timelineCursorTime(cursor)
	}
	fetch := limit * 6
	if fetch < 120 {
		fetch = 120
	}
	if fetch > 500 {
		fetch = 500
	}

	var items []models.TimelineItem
	logsOK := timelineKindWanted(filter, "review") || timelineKindWanted(filter, "log")
	sessionsOK := timelineKindWanted(filter, "session")
	socialOK := timelineKindWanted(filter, "shelf") || timelineKindWanted(filter, "list") ||
		timelineKindWanted(filter, "like") || timelineKindWanted(filter, "rating") || timelineKindWanted(filter, "follow")

	if logsOK {
		conds := []string{
			"l.user_id IN (" + placeholders(len(ownerIDs)) + ")",
			"l.deleted_at IS NULL",
			"(l.draft=0 OR l.user_id=?)",
		}
		args := append(int64Args(ownerIDs), viewerID)
		switch filter {
		case "reviews":
			conds = append(conds, "l.review<>''")
		case "logs":
			conds = append(conds, "l.review=''", "l.auto_created=0")
		default:
			conds = append(conds, "(l.review<>'' OR (l.review='' AND l.auto_created=0))")
		}
		if hasCursor {
			conds = append(conds, "(l.created_at < ? OR (l.created_at = ? AND printf('%020d',l.id) < ?))")
			args = append(args, before.UTC().Format(time.RFC3339), before.UTC().Format(time.RFC3339), timelineCursorID(beforeKey))
		}
		args = append(args, fetch)
		rows, err := s.DB.QueryContext(ctx, `SELECT `+logCols+` FROM logs l WHERE `+strings.Join(conds, " AND ")+`
			ORDER BY l.created_at DESC,l.id DESC LIMIT ?`, args...)
		if err != nil {
			return nil, "", err
		}
		for rows.Next() {
			l, err := scanLog(rows)
			if err != nil {
				rows.Close()
				return nil, "", err
			}
			if l.AutoCreated {
				continue
			}
			kind := "log"
			if l.Review != "" {
				kind = "review"
			}
			if !timelineKindWanted(filter, kind) {
				continue
			}
			items = append(items, models.TimelineItem{
				Key: timelineKey("log", l.ID), Kind: kind, At: l.CreatedAt, Log: l,
			})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, "", err
		}
	}

	if sessionsOK {
		conds := []string{
			"l.user_id IN (" + placeholders(len(ownerIDs)) + ")",
			"l.deleted_at IS NULL",
			"(l.draft=0 OR l.user_id=?)",
			"ps.note<>''",
		}
		args := append(int64Args(ownerIDs), viewerID)
		if hasCursor {
			conds = append(conds, "(ps.created_at < ? OR (ps.created_at = ? AND printf('%020d',ps.id) < ?))")
			args = append(args, before.UTC().Format(time.RFC3339), before.UTC().Format(time.RFC3339), timelineCursorID(beforeKey))
		}
		args = append(args, fetch)
		rows, err := s.DB.QueryContext(ctx, `SELECT ps.id,ps.log_id,ps.note,ps.note_privacy,ps.playtime,
			COALESCE(ps.started_on,''),COALESCE(ps.ended_on,''),ps.created_at
			FROM play_sessions ps JOIN logs l ON l.id=ps.log_id
			WHERE `+strings.Join(conds, " AND ")+`
			ORDER BY ps.created_at DESC,ps.id DESC LIMIT ?`, args...)
		if err != nil {
			return nil, "", err
		}
		for rows.Next() {
			session := &models.Session{}
			var playedOn, endedOn, created string
			if err := rows.Scan(&session.ID, &session.LogID, &session.Note, &session.NotePrivacy,
				&session.Playtime, &playedOn, &endedOn, &created); err != nil {
				rows.Close()
				return nil, "", err
			}
			session.StartedOn, session.EndedOn = playedOn, endedOn
			session.CreatedAt = parseTime(created)
			items = append(items, models.TimelineItem{
				Key: timelineKey("session", session.ID), Kind: "session", At: session.CreatedAt, Session: session,
			})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, "", err
		}
	}

	if socialOK {
		types := []string{}
		if timelineKindWanted(filter, "shelf") {
			types = append(types, "status")
		}
		if timelineKindWanted(filter, "list") {
			types = append(types, "list_add")
		}
		if timelineKindWanted(filter, "like") {
			types = append(types, "liked")
		}
		if timelineKindWanted(filter, "rating") {
			types = append(types, "rated")
		}
		if timelineKindWanted(filter, "follow") {
			types = append(types, "follow")
		}
		if len(types) > 0 {
			conds := []string{
				"a.user_id IN (" + placeholders(len(ownerIDs)) + ")",
				"a.type IN (" + placeholders(len(types)) + ")",
			}
			args := append(int64Args(ownerIDs), stringArgs(types)...)
			if hasCursor {
				conds = append(conds, "(a.created_at < ? OR (a.created_at = ? AND printf('%020d',a.id) < ?))")
				args = append(args, before.UTC().Format(time.RFC3339), before.UTC().Format(time.RFC3339), timelineCursorID(beforeKey))
			}
			args = append(args, fetch)
			rows, err := s.DB.QueryContext(ctx, `SELECT a.id,a.user_id,a.type,a.game_id,a.log_id,a.list_id,
				a.target_user,a.target_name,a.created_at FROM activities a
				WHERE `+strings.Join(conds, " AND ")+` ORDER BY a.created_at DESC,a.id DESC LIMIT ?`, args...)
			if err != nil {
				return nil, "", err
			}
			for rows.Next() {
				var (
					item                  models.TimelineItem
					activityID            int64
					activityType          string
					gameID, logID, listID sql.NullInt64
					targetUser            sql.NullInt64
					created               string
				)
				if err := rows.Scan(&activityID, &item.UserID, &activityType, &gameID, &logID, &listID,
					&targetUser, &item.TargetName, &created); err != nil {
					rows.Close()
					return nil, "", err
				}
				item.Kind = timelineKindForActivityType(activityType)
				item.Visibility = activityType
				if gameID.Valid {
					item.GameID = &gameID.Int64
				}
				if listID.Valid {
					item.ListID = &listID.Int64
				}
				if targetUser.Valid {
					item.TargetUserID = &targetUser.Int64
				}
				item.At = parseTime(created)
				item.Key = timelineKey("activity", activityID)
				items = append(items, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, "", err
			}
		}
	}

	if len(items) == 0 {
		return nil, "", nil
	}

	// Owners and viewer are needed for the master privacy switches and adult
	// gating. Load them before hydrating content.
	owners := map[int64]*models.User{}
	for _, id := range ownerIDs {
		if u, err := s.UserByID(ctx, id); err == nil {
			owners[id] = u
		}
	}
	var viewer *models.User
	if viewerID != 0 {
		viewer, _ = s.UserByID(ctx, viewerID)
	}
	visibility := map[int64]map[string]string{}
	for _, id := range ownerIDs {
		if m, err := s.ActivityVisibility(ctx, id); err == nil {
			visibility[id] = m
		}
	}
	games := map[int64]*models.Game{}
	lists := map[int64]*models.List{}
	targetUsers := map[int64]*models.User{}

	for i := range items {
		item := &items[i]
		switch item.Kind {
		case "review", "log":
			if item.Log != nil {
				s.hydrateLog(ctx, item.Log)
				item.User = item.Log.User
				item.Game = item.Log.Game
				if item.Log.GameID != 0 {
					item.GameID = &item.Log.GameID
				}
			}
		case "session":
			if item.Session != nil {
				item.Session.Days, _ = s.sessionStrings(ctx, "play_session_days", "day", item.Session.ID)
				item.Session.Tags, _ = s.sessionStrings(ctx, "play_session_tags", "tag", item.Session.ID)
				if parent, err := s.LogByID(ctx, item.Session.LogID); err == nil {
					item.Log = parent
					item.User = parent.User
					item.Game = parent.Game
					if parent.GameID != 0 {
						item.GameID = &parent.GameID
					}
				}
			}
		case "shelf", "like", "rating", "list", "follow":
			item.User = owners[item.UserID]
			if item.GameID != nil {
				g := games[*item.GameID]
				if g == nil {
					g, _ = s.GameByID(ctx, *item.GameID)
					games[*item.GameID] = g
				}
				item.Game = g
			}
			if item.ListID != nil {
				l := lists[*item.ListID]
				if l == nil {
					l, _ = s.ListByID(ctx, *item.ListID, false)
					lists[*item.ListID] = l
				}
				item.List = l
			}
			if item.TargetUserID != nil {
				u := targetUsers[*item.TargetUserID]
				if u == nil {
					u, _ = s.UserByID(ctx, *item.TargetUserID)
					targetUsers[*item.TargetUserID] = u
				}
				item.TargetUser = u
			}
		}
	}

	filtered := items[:0]
	for i := range items {
		item := items[i]
		owner := owners[item.UserID]
		if item.User != nil {
			owner = item.User
		}
		if owner == nil {
			continue
		}
		item.UserID = owner.ID
		if !timelineVisible(ctx, s, item, viewer, owner, visibility[owner.ID]) {
			continue
		}
		filtered = append(filtered, item)
	}
	items = filtered

	sort.Slice(items, func(i, j int) bool {
		if items[i].At.Equal(items[j].At) {
			return items[i].Key > items[j].Key
		}
		return items[i].At.After(items[j].At)
	})
	next := ""
	if len(items) > limit {
		items = items[:limit]
	}
	if len(items) == limit && len(items) > 0 {
		last := items[len(items)-1]
		next = last.At.UTC().Format(time.RFC3339Nano) + "|" + last.Key
	}
	return items, next, nil
}

func timelineVisible(ctx context.Context, s *Store, item models.TimelineItem, viewer, owner *models.User, visibility map[string]string) bool {
	if owner == nil || item.UserID != owner.ID {
		return false
	}
	isOwner := viewer != nil && viewer.ID == owner.ID
	if !isOwner {
		if owner.HideActivity {
			return false
		}
		if viewer != nil && s.IsBlocked(ctx, viewer.ID, owner.ID) {
			return false
		}
	}

	// Adult games need the same opt-in on every surface.
	if item.Game != nil && item.Game.Adult && !isOwner && (viewer == nil || !viewer.AdultOptIn) {
		return false
	}

	if item.Kind == "session" {
		if item.Session == nil || item.Session.Note == "" || item.Log == nil {
			return false
		}
		friend := viewer != nil && !isOwner && s.Mutual(ctx, viewer.ID, owner.ID)
		if !isOwner && !noteVisibleTo(item.Session.NotePrivacy, friend) {
			return false
		}
	}
	if item.Kind == "review" || item.Kind == "log" {
		if item.Log == nil || item.Log.Review == "" && item.Log.AutoCreated {
			return false
		}
		if item.Log.Draft && !isOwner {
			return false
		}
	}
	if item.Kind == "list" {
		if item.List == nil {
			return false
		}
		switch item.List.Privacy {
		case "private":
			if !isOwner {
				return false
			}
		case "personal":
			if !isOwner {
				if viewer == nil {
					return false
				}
				ok, approved := s.IsFollowing(ctx, viewer.ID, owner.ID)
				if !ok || !approved {
					return false
				}
			}
		}
	}
	if item.Kind == "shelf" || item.Kind == "like" || item.Kind == "rating" || item.Kind == "list" {
		if item.Game == nil {
			return false
		}
	}
	if item.Kind == "follow" && item.TargetUser == nil {
		return false
	}
	if isOwner {
		return true
	}

	level := visibility[item.Kind]
	if level == "" {
		level = DefaultActivityVisibility[item.Kind]
	}
	switch level {
	case "private":
		return false
	case "followers":
		if viewer == nil {
			return false
		}
		ok, approved := s.IsFollowing(ctx, viewer.ID, owner.ID)
		return ok && approved
	default:
		return true
	}
}

func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func stringArgs(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
