// Package backloggd reads Backloggd library exports. Backloggd's game ids are
// IGDB ids, which lets an import resolve (and fetch) games without fuzzy title
// matching. JSON exports contain playthroughs and reviews; CSV exports contain
// the same shelf/rating data in a flatter form.
package backloggd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Entry is one game in a Backloggd library, normalized for LudusLog.
type Entry struct {
	IGDBID     int64
	Title      string
	Slug       string
	Played     bool
	Playing    bool
	Backlog    bool
	Wishlist   bool
	Liked      bool
	Rating     float64 // 0–10, half points allowed
	Mastered   bool
	Replay     bool
	Review     string
	Spoiler    bool
	StartedAt  string // YYYY-MM-DD
	FinishedAt string // YYYY-MM-DD
	Minutes    int
}

// Parse detects a Backloggd JSON or CSV export from its contents.
func Parse(r io.Reader, filename string) ([]Entry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("backloggd: empty export")
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		entries, err := parseJSON(trimmed)
		if err != nil {
			return nil, fmt.Errorf("backloggd JSON: %w", err)
		}
		return entries, nil
	}
	entries, err := parseCSV(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("backloggd CSV: %w", err)
	}
	if len(entries) == 0 && strings.HasSuffix(strings.ToLower(filename), ".json") {
		return nil, fmt.Errorf("backloggd: no games found in JSON export")
	}
	return entries, nil
}

type jsonExport struct {
	ID       string                     `json:"id"`
	Name     string                     `json:"name"`
	GameLog  jsonLog                    `json:"game_log"`
	Plays    map[string]jsonPlaythrough `json:"playthroughs"`
	RecentID *int64                     `json:"most_recent_playthrough_id"`
}

type jsonLog struct {
	Status     string  `json:"status"`
	Rating     float64 `json:"rating"`
	IsPlay     bool    `json:"is_play"`
	IsPlaying  bool    `json:"is_playing"`
	IsBacklog  bool    `json:"is_backlog"`
	IsWishlist bool    `json:"is_wishlist"`
	GameLiked  bool    `json:"game_liked"`
	TotalHours int     `json:"total_hours"`
	TotalMins  int     `json:"total_minutes"`
	LastEdited string  `json:"last_edited_at"`
}

type jsonPlaythrough struct {
	ID             int64   `json:"id"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	Rating         float64 `json:"rating"`
	Review         string  `json:"review"`
	ReviewSpoilers bool    `json:"review_spoilers"`
	HoursPlayed    int     `json:"hours_played"`
	MinsPlayed     int     `json:"mins_played"`
	HoursFinished  int     `json:"hours_finished"`
	MinsFinished   int     `json:"mins_finished"`
	HoursMastered  int     `json:"hours_mastered"`
	MinsMastered   int     `json:"mins_mastered"`
	IsMaster       bool    `json:"is_master"`
	IsReplay       bool    `json:"is_replay"`
	StartDate      *string `json:"start_date"`
	FinishDate     *string `json:"finish_date"`
}

func parseJSON(data []byte) ([]Entry, error) {
	var raw []jsonExport
	if err := json.Unmarshal(data, &raw); err != nil {
		// Some tools wrap the array in an object.
		var wrapped struct {
			Games []jsonExport `json:"games"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil || len(wrapped.Games) == 0 {
			return nil, err
		}
		raw = wrapped.Games
	}
	entries := make([]Entry, 0, len(raw))
	for _, g := range raw {
		id, _ := strconv.ParseInt(strings.TrimSpace(g.ID), 10, 64)
		e := Entry{
			IGDBID:   id,
			Title:    strings.TrimSpace(g.Name),
			Played:   g.GameLog.IsPlay,
			Playing:  g.GameLog.IsPlaying,
			Backlog:  g.GameLog.IsBacklog,
			Wishlist: g.GameLog.IsWishlist,
			Liked:    g.GameLog.GameLiked,
			Rating:   g.GameLog.Rating,
			Minutes:  g.GameLog.TotalHours*60 + g.GameLog.TotalMins,
		}
		plays := sortedPlays(g.Plays)
		if e.Rating == 0 {
			for _, p := range plays {
				if p.Rating > 0 {
					e.Rating = p.Rating
					break
				}
			}
		}
		for _, p := range plays {
			if strings.TrimSpace(p.Review) != "" {
				e.Review = strings.TrimSpace(p.Review)
				e.Spoiler = p.ReviewSpoilers
				break
			}
		}
		if len(plays) > 1 {
			e.Replay = true
		}
		for _, p := range plays {
			if p.IsReplay {
				e.Replay = true
			}
			if p.IsMaster {
				e.Mastered = true
			}
			if p.StartDate != nil && *p.StartDate != "" && (e.StartedAt == "" || *p.StartDate < e.StartedAt) {
				e.StartedAt = *p.StartDate
			}
			if p.FinishDate != nil && *p.FinishDate != "" && *p.FinishDate > e.FinishedAt {
				e.FinishedAt = *p.FinishDate
			}
			if e.Minutes == 0 {
				// Playthrough time is the best fallback when the aggregate
				// collection timer was never used.
				switch {
				case p.HoursMastered > 0 || p.MinsMastered > 0:
					e.Minutes = p.HoursMastered*60 + p.MinsMastered
				case p.HoursFinished > 0 || p.MinsFinished > 0:
					e.Minutes = p.HoursFinished*60 + p.MinsFinished
				default:
					e.Minutes = p.HoursPlayed*60 + p.MinsPlayed
				}
			}
		}
		if e.Minutes == 0 {
			e.Minutes = g.GameLog.TotalHours*60 + g.GameLog.TotalMins
		}
		if e.Title == "" {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// sortedPlays returns playthroughs newest first. The JSON object key order is
// meaningless, so use the editable timestamps and fall back to the numeric id.
func sortedPlays(in map[string]jsonPlaythrough) []jsonPlaythrough {
	out := make([]jsonPlaythrough, 0, len(in))
	for _, p := range in {
		out = append(out, p)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if playTime(out[j]) > playTime(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func playTime(p jsonPlaythrough) int64 {
	for _, s := range []string{p.UpdatedAt, p.CreatedAt} {
		if t, err := time.Parse("Jan 2 2006", strings.TrimSpace(s)); err == nil {
			return t.Unix()
		}
	}
	return p.ID
}

func parseCSV(r io.Reader) ([]Entry, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	header := make(map[string]int, len(rows[0]))
	backloggdFormat := false
	for i, h := range rows[0] {
		key := normalizeHeader(h)
		header[key] = i
		if key == "game_name" || key == "backloggd_url" || key == "played" {
			backloggdFormat = true
		}
	}
	out := make([]Entry, 0, len(rows)-1)
	for _, row := range rows[1:] {
		get := func(keys ...string) string {
			for _, k := range keys {
				if i, ok := header[k]; ok {
					return strings.TrimSpace(at(row, i))
				}
			}
			return ""
		}
		title := get("game_name", "title", "name", "game")
		if title == "" {
			continue
		}
		id, _ := strconv.ParseInt(get("id", "igdb_id", "backloggd_id"), 10, 64)
		e := Entry{
			IGDBID:     id,
			Title:      title,
			Slug:       slugFromURL(get("backloggd_url", "url")),
			Played:     boolValue(get("played")),
			Playing:    boolValue(get("currently_playing", "playing")),
			Backlog:    boolValue(get("in_backlog", "backlog")),
			Wishlist:   boolValue(get("in_wishlist", "wishlist")),
			Liked:      boolValue(get("liked")),
			Mastered:   boolValue(get("mastered")),
			Replay:     boolValue(get("replay")),
			Review:     get("review"),
			Spoiler:    boolValue(get("review_contains_spoilers", "spoiler")),
			StartedAt:  dateValue(get("start_date", "started_at", "started")),
			FinishedAt: dateValue(get("finish_date", "finished_at", "finished")),
			Rating:     floatValue(get("rating")),
		}
		// A generic title,rating,review CSV is treated as a played list.
		if !backloggdFormat && !e.Played && !e.Playing && !e.Backlog && !e.Wishlist {
			e.Played = true
		}
		if !backloggdFormat && e.Rating > 0 && e.Rating <= 5 {
			// The legacy importer treated five-point CSVs as five stars.
			e.Rating *= 2
		}
		minutes := intValue(get("total_hours"))*60 + intValue(get("total_minutes"))
		if minutes == 0 {
			minutes = intValue(get("playthrough_hours_to_master"))*60 + intValue(get("playthrough_minutes_to_master"))
		}
		if minutes == 0 {
			minutes = intValue(get("playthrough_hours_to_finish"))*60 + intValue(get("playthrough_minutes_to_finish"))
		}
		if minutes == 0 {
			minutes = intValue(get("playthrough_hours"))*60 + intValue(get("playthrough_minutes"))
		}
		e.Minutes = minutes
		out = append(out, e)
	}
	return out, nil
}

func normalizeHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\uFEFF")))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func at(row []string, i int) string {
	if i >= 0 && i < len(row) {
		return row[i]
	}
	return ""
}

func boolValue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y":
		return true
	}
	return false
}

func intValue(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func floatValue(s string) float64 {
	n, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if n < 0 {
		return 0
	}
	return n
}

func dateValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", s); err == nil {
		return s
	}
	for _, layout := range []string{"Jan 2, 2006", "Jan 2 2006", "2006/01/02", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

func slugFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "games" {
		return parts[1]
	}
	return ""
}
